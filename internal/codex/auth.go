// Package codex implementa la autenticación OAuth 2.0 PKCE, gestión de sesiones
// y el cliente de transporte para la suscripción de OpenAI Codex (ChatGPT).
package codex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// ClientID es el identificador oficial de la aplicación OpenAI Codex CLI.
	ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

	// AuthorizeURL es el punto de inicio del flujo OAuth en el navegador.
	AuthorizeURL = "https://auth.openai.com/oauth/authorize"

	// TokenURL es el endpoint donde se intercambia el código y se refrescan tokens.
	TokenURL = "https://auth.openai.com/oauth/token"

	// RedirectURI es la URI de redirección loopback local registrada por Codex.
	RedirectURI = "http://localhost:1455/auth/callback"

	// DefaultBaseURL es el endpoint base para la API de suscripción de Codex.
	DefaultBaseURL = "https://chatgpt.com/backend-api/codex"

	// Scope solicitado para acceder a la identidad y suscripción del usuario.
	Scope = "openid profile email offline_access"
)

// TokenBundle almacena las credenciales de sesión de la suscripción de Codex.
type TokenBundle struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	ExpiresAt    int64  `json:"expires_at"` // Timestamp Unix en segundos
}

// codexFileSchema representa la estructura usada en ~/.codex/auth.json.
type codexFileSchema struct {
	AuthMode     string       `json:"auth_mode,omitempty"`
	APIKey       string       `json:"OPENAI_API_KEY,omitempty"`
	Tokens       *TokenBundle `json:"tokens,omitempty"`
	AccessToken  string       `json:"access_token,omitempty"`
	RefreshToken string       `json:"refresh_token,omitempty"`
	IDToken      string       `json:"id_token,omitempty"`
	AccountID    string       `json:"account_id,omitempty"`
	LastRefresh  string       `json:"last_refresh,omitempty"`
}

// LocalAuthPath devuelve la ruta absoluta del archivo auth.json de Codex.
func LocalAuthPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("CODEX_HOME")); p != "" {
		return filepath.Join(p, "auth.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no se pudo determinar el directorio home: %w", err)
	}
	return filepath.Join(home, ".codex", "auth.json"), nil
}

// LoadLocalAuth lee las credenciales existentes en ~/.codex/auth.json.
func LoadLocalAuth() (TokenBundle, error) {
	path, err := LocalAuthPath()
	if err != nil {
		return TokenBundle{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return TokenBundle{}, fmt.Errorf("no se pudo leer %s: %w", path, err)
	}

	var schema codexFileSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		return TokenBundle{}, fmt.Errorf("archivo auth.json con formato inválido: %w", err)
	}

	var bundle TokenBundle
	if schema.Tokens != nil {
		bundle = *schema.Tokens
	} else {
		bundle = TokenBundle{
			AccessToken:  schema.AccessToken,
			RefreshToken: schema.RefreshToken,
			IDToken:      schema.IDToken,
			AccountID:    schema.AccountID,
		}
	}

	// Si no hay access_token pero hay APIKey manual en el archivo
	if bundle.AccessToken == "" && strings.TrimSpace(schema.APIKey) != "" {
		bundle.AccessToken = strings.TrimSpace(schema.APIKey)
	}

	if bundle.AccessToken == "" && bundle.RefreshToken == "" {
		return TokenBundle{}, errors.New("no se encontraron tokens válidos en auth.json")
	}

	// Intenta extraer expiración y account_id si faltan
	if bundle.IDToken != "" || bundle.AccessToken != "" {
		acc, exp := ExtractClaims(firstNonEmpty(bundle.IDToken, bundle.AccessToken))
		if bundle.AccountID == "" {
			bundle.AccountID = acc
		}
		if bundle.ExpiresAt == 0 {
			bundle.ExpiresAt = exp
		}
	}

	return bundle, nil
}

// SaveLocalAuth guarda o actualiza las credenciales en ~/.codex/auth.json de forma atómica.
func SaveLocalAuth(bundle TokenBundle) error {
	path, err := LocalAuthPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("no se pudo crear directorio %s: %w", dir, err)
	}

	schema := codexFileSchema{
		AuthMode:    "chatgpt",
		Tokens:      &bundle,
		LastRefresh: time.Now().UTC().Format(time.RFC3339Nano),
	}

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("no se pudo serializar auth.json: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".auth-*.json")
	if err != nil {
		return fmt.Errorf("no se pudo crear archivo temporal: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, path)
}

// ExtractClaims decodifica los claims del JWT (sin verificar firma) para extraer exp y account_id.
func ExtractClaims(tokenStr string) (accountID string, expiresAt int64) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) < 2 {
		return "", 0
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Algunos tokens usan padding estándar
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return "", 0
		}
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return "", 0
	}

	if expVal, ok := raw["exp"].(float64); ok {
		expiresAt = int64(expVal)
	}

	// Buscar ChatGPT Account ID en el claim estándar https://api.openai.com/auth
	if authMap, ok := raw["https://api.openai.com/auth"].(map[string]interface{}); ok {
		if acc, ok := authMap["chatgpt_account_id"].(string); ok && acc != "" {
			accountID = acc
		} else if acc, ok := authMap["account_id"].(string); ok && acc != "" {
			accountID = acc
		}
	}

	if accountID == "" {
		if acc, ok := raw["chatgpt_account_id"].(string); ok && acc != "" {
			accountID = acc
		} else if acc, ok := raw["account_id"].(string); ok && acc != "" {
			accountID = acc
		}
	}

	return accountID, expiresAt
}

// tokenResponse define la estructura devuelta por POST /oauth/token.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// RefreshAccessToken solicita un nuevo par access_token/refresh_token en el endpoint de OpenAI.
func RefreshAccessToken(ctx context.Context, refreshToken string) (TokenBundle, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return TokenBundle{}, errors.New("refresh token vacío")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", ClientID)
	form.Set("refresh_token", strings.TrimSpace(refreshToken))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenBundle{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := codexHTTPClient().Do(req)
	if err != nil {
		return TokenBundle{}, fmt.Errorf("error al conectar con %s: %w", TokenURL, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return TokenBundle{}, fmt.Errorf("respuesta inválida de token endpoint (HTTP %d): %s", resp.StatusCode, string(body))
	}

	if tr.Error != "" {
		return TokenBundle{}, fmt.Errorf("error de autorización (%s): %s", tr.Error, tr.ErrorDesc)
	}
	if resp.StatusCode != http.StatusOK {
		return TokenBundle{}, fmt.Errorf("token endpoint devolvió HTTP %d: %s", resp.StatusCode, string(body))
	}

	acc, exp := ExtractClaims(firstNonEmpty(tr.IDToken, tr.AccessToken))
	expiresAt := exp
	if expiresAt == 0 && tr.ExpiresIn > 0 {
		expiresAt = time.Now().Unix() + tr.ExpiresIn
	}

	return TokenBundle{
		AccessToken:  tr.AccessToken,
		RefreshToken: firstNonEmpty(tr.RefreshToken, refreshToken),
		IDToken:      tr.IDToken,
		AccountID:    acc,
		ExpiresAt:    expiresAt,
	}, nil
}

// LoginPKCE ejecuta el flujo interactivo OAuth 2.0 PKCE abriendo el navegador.
// onAuthURL es invocado con la URL que el usuario debe autorizar.
func LoginPKCE(ctx context.Context, onAuthURL func(string)) (TokenBundle, error) {
	// 1. Generar code_verifier (32 bytes aleatorios base64url)
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return TokenBundle{}, fmt.Errorf("error al generar verifier: %w", err)
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// 2. Generar code_challenge (SHA256 del verifier)
	challengeHash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])

	// 3. Generar state aleatorio
	stateBytes := make([]byte, 16)
	rand.Read(stateBytes)
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	// 4. Iniciar listener HTTP local en puerto 1455
	listener, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return TokenBundle{}, fmt.Errorf("no se pudo abrir puerto 1455 para el callback de autenticación: %w", err)
	}
	defer listener.Close()

	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if qErr := query.Get("error"); qErr != "" {
			errDesc := query.Get("error_description")
			http.Error(w, fmt.Sprintf("Error de autorización: %s (%s)", qErr, errDesc), http.StatusBadRequest)
			errChan <- fmt.Errorf("autorización rechazada: %s", qErr)
			return
		}

		if query.Get("state") != state {
			http.Error(w, "Estado OAuth inválido", http.StatusBadRequest)
			errChan <- errors.New("discrepancia de estado OAuth CSRF")
			return
		}

		code := query.Get("code")
		if code == "" {
			http.Error(w, "Falta el código de autorización", http.StatusBadRequest)
			errChan <- errors.New("código ausente en callback")
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>MKD — Autenticación Exitosa</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; background: #0f172a; color: #f8fafc; }
    .card { background: #1e293b; border: 1px solid #334155; border-radius: 12px; padding: 40px; text-align: center; max-width: 420px; box-shadow: 0 10px 25px rgba(0,0,0,0.5); }
    h1 { color: #38bdf8; font-size: 24px; margin-bottom: 12px; }
    p { color: #94a3b8; font-size: 15px; line-height: 1.5; margin-bottom: 0; }
  </style>
</head>
<body>
  <div class="card">
    <h1>✓ Sesión Iniciada en MKD</h1>
    <p>La autenticación con tu cuenta de Codex/ChatGPT se completó exitosamente.<br><br>Ya puedes cerrar esta pestaña y regresar a la terminal.</p>
  </div>
</body>
</html>`)

		codeChan <- code
	})

	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()
	defer server.Shutdown(context.Background())

	// 5. Construir URL de autorización
	authVals := url.Values{}
	authVals.Set("response_type", "code")
	authVals.Set("client_id", ClientID)
	authVals.Set("redirect_uri", RedirectURI)
	authVals.Set("scope", Scope)
	authVals.Set("code_challenge", codeChallenge)
	authVals.Set("code_challenge_method", "S256")
	authVals.Set("state", state)
	authVals.Set("codex_cli_simplified_flow", "true")
	authVals.Set("id_token_add_organizations", "true")

	fullAuthURL := AuthorizeURL + "?" + authVals.Encode()
	if onAuthURL != nil {
		onAuthURL(fullAuthURL)
	}

	// 6. Intentar abrir en navegador
	_ = openBrowser(fullAuthURL)

	// 7. Esperar callback o cancelación de contexto
	var authCode string
	select {
	case <-ctx.Done():
		return TokenBundle{}, ctx.Err()
	case err := <-errChan:
		return TokenBundle{}, err
	case authCode = <-codeChan:
	}

	// 8. Intercambiar código por tokens
	tokenVals := url.Values{}
	tokenVals.Set("grant_type", "authorization_code")
	tokenVals.Set("client_id", ClientID)
	tokenVals.Set("code", authCode)
	tokenVals.Set("code_verifier", codeVerifier)
	tokenVals.Set("redirect_uri", RedirectURI)

	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(tokenVals.Encode()))
	if err != nil {
		return TokenBundle{}, err
	}
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	tokenResp, err := codexHTTPClient().Do(tokenReq)
	if err != nil {
		return TokenBundle{}, fmt.Errorf("error al intercambiar código: %w", err)
	}
	defer tokenResp.Body.Close()

	body, _ := io.ReadAll(tokenResp.Body)
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return TokenBundle{}, fmt.Errorf("respuesta de token no legible (HTTP %d): %s", tokenResp.StatusCode, string(body))
	}
	if tr.Error != "" {
		return TokenBundle{}, fmt.Errorf("error de token (%s): %s", tr.Error, tr.ErrorDesc)
	}
	if tokenResp.StatusCode != http.StatusOK {
		return TokenBundle{}, fmt.Errorf("token endpoint devolvió HTTP %d: %s", tokenResp.StatusCode, string(body))
	}

	acc, exp := ExtractClaims(firstNonEmpty(tr.IDToken, tr.AccessToken))
	expiresAt := exp
	if expiresAt == 0 && tr.ExpiresIn > 0 {
		expiresAt = time.Now().Unix() + tr.ExpiresIn
	}

	return TokenBundle{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
		AccountID:    acc,
		ExpiresAt:    expiresAt,
	}, nil
}

func openBrowser(targetURL string) error {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("xdg-open", targetURL).Start()
	case "darwin":
		return exec.Command("open", targetURL).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL).Start()
	default:
		return errors.New("sistema operativo no soportado para auto-abrir navegador")
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
