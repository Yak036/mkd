// Package claude implementa la autenticación OAuth 2.0 PKCE, gestión de sesiones
// y el cliente de transporte para la suscripción de Claude (Anthropic Messages API).
package claude

import (
	"bufio"
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
	// ClientID es el identificador oficial utilizado por la CLI de Claude Code.
	ClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

	// AuthorizeURL es la URL de inicio del flujo de autorización de Claude.
	AuthorizeURL = "https://claude.ai/oauth/authorize"

	// TokenURL es el endpoint donde se intercambia el código por tokens y se refrescan.
	TokenURL = "https://console.anthropic.com/v1/oauth/token"

	// RedirectURICallback es la URI de callback web oficial de Anthropic.
	RedirectURICallback = "https://console.anthropic.com/oauth/code/callback"

	// LocalCallbackURI es la URI de redirección loopback local para interceptar el código automáticamente.
	LocalCallbackURI = "http://localhost:54321/callback"

	// DefaultBaseURL es el endpoint base para la API de Claude.
	DefaultBaseURL = "https://api.anthropic.com/v1"

	// DefaultModel es el modelo insignia por defecto de Claude.
	DefaultModel = "claude-3-7-sonnet-20250219"

	// Scope solicitado para inferencia y perfil de usuario.
	Scope = "org:create_api_key user:profile user:inference"

	// AnthropicVersion define la versión de API requerida por Anthropic.
	AnthropicVersion = "2023-06-01"

	// AnthropicOAuthBetaHeader habilita el soporte de tokens OAuth en Messages API.
	AnthropicOAuthBetaHeader = "oauth-2025-01-22"
)

// TokenBundle almacena las credenciales de la suscripción de Claude.
type TokenBundle struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"` // Timestamp Unix en segundos
	TokenType    string `json:"token_type,omitempty"`
}

// claudeFileSchema maneja múltiples esquemas conocidos de archivos de credenciales de Claude.
type claudeFileSchema struct {
	OAuthToken    string `json:"oauthToken,omitempty"`
	RefreshToken  string `json:"refreshToken,omitempty"`
	RefreshToken2 string `json:"refresh_token,omitempty"`
	ExpiresAt     int64  `json:"expiresAt,omitempty"`
	ExpiresAt2    int64  `json:"expires_at,omitempty"`
	PrimaryAPIKey string `json:"primaryApiKey,omitempty"`
	AccessToken   string `json:"access_token,omitempty"`
	Session       *struct {
		Token string `json:"token,omitempty"`
	} `json:"session,omitempty"`
	OAuthAccount *struct {
		Token        string `json:"token,omitempty"`
		RefreshToken string `json:"refreshToken,omitempty"`
	} `json:"oauthAccount,omitempty"`
}

// LocalAuthPaths devuelve los posibles archivos de credenciales locales en orden de prioridad.
func LocalAuthPaths() ([]string, error) {
	var paths []string

	if p := strings.TrimSpace(os.Getenv("CLAUDE_HOME")); p != "" {
		paths = append(paths, filepath.Join(p, "credentials.json"), filepath.Join(p, ".credentials.json"))
	}
	if p := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); p != "" {
		paths = append(paths, filepath.Join(p, "credentials.json"), filepath.Join(p, ".credentials.json"))
	}

	home, err := os.UserHomeDir()
	if err == nil {
		paths = append(paths,
			filepath.Join(home, ".mkd", "claude_auth.json"),
			filepath.Join(home, ".claude", ".credentials.json"),
			filepath.Join(home, ".claude", "credentials.json"),
			filepath.Join(home, ".claude.json"),
			filepath.Join(home, ".config", "claude", "credentials.json"),
		)
	}

	return paths, nil
}

// LoadLocalAuth busca e importa una sesión existente desde el entorno o disco.
func LoadLocalAuth() (TokenBundle, error) {
	// 1. Verificar variables de entorno
	if envToken := strings.TrimSpace(os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")); envToken != "" {
		return TokenBundle{AccessToken: envToken}, nil
	}
	if envKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); envKey != "" {
		return TokenBundle{AccessToken: envKey}, nil
	}

	// 2. Buscar en las rutas estándar de credenciales
	paths, err := LocalAuthPaths()
	if err != nil {
		return TokenBundle{}, err
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}

		var schema claudeFileSchema
		if err := json.Unmarshal(data, &schema); err != nil {
			continue
		}

		token := firstNonEmpty(schema.OAuthToken, schema.AccessToken, schema.PrimaryAPIKey)
		if token == "" && schema.Session != nil {
			token = schema.Session.Token
		}
		if token == "" && schema.OAuthAccount != nil {
			token = schema.OAuthAccount.Token
		}

		if token != "" {
			refreshToken := firstNonEmpty(schema.RefreshToken, schema.RefreshToken2)
			if refreshToken == "" && schema.OAuthAccount != nil {
				refreshToken = schema.OAuthAccount.RefreshToken
			}

			expiresAt := schema.ExpiresAt
			if expiresAt == 0 {
				expiresAt = schema.ExpiresAt2
			}
			// Si viene en milisegundos (timestamp JS), normalizar a segundos
			if expiresAt > 1000000000000 {
				expiresAt = expiresAt / 1000
			}

			return TokenBundle{
				AccessToken:  strings.TrimSpace(token),
				RefreshToken: strings.TrimSpace(refreshToken),
				ExpiresAt:    expiresAt,
			}, nil
		}
	}

	return TokenBundle{}, errors.New("no se encontraron credenciales previas de Claude en el sistema")
}

// SaveLocalAuth guarda una copia de respaldo de las credenciales en ~/.mkd/claude_auth.json con permisos 0600.
func SaveLocalAuth(bundle TokenBundle) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".mkd")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	target := filepath.Join(dir, "claude_auth.json")

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, 0600)
}

// tokenResponse representa la respuesta devuelta por POST /v1/oauth/token de Anthropic.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// RefreshAccessToken solicita un nuevo par access_token/refresh_token en el endpoint de Anthropic.
func RefreshAccessToken(ctx context.Context, refreshToken string) (TokenBundle, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return TokenBundle{}, errors.New("refresh token de Claude vacío")
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

	resp, err := claudeHTTPClient().Do(req)
	if err != nil {
		return TokenBundle{}, fmt.Errorf("error al conectar con %s: %w", TokenURL, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return TokenBundle{}, fmt.Errorf("respuesta de token no parseable (HTTP %d): %s", resp.StatusCode, string(body))
	}

	if tr.Error != "" {
		return TokenBundle{}, fmt.Errorf("error al refrescar token de Claude: %s (%s)", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return TokenBundle{}, fmt.Errorf("el servidor no devolvió access_token (HTTP %d): %s", resp.StatusCode, string(body))
	}

	newRefresh := tr.RefreshToken
	if newRefresh == "" {
		newRefresh = refreshToken
	}

	var expiresAt int64
	if tr.ExpiresIn > 0 {
		expiresAt = time.Now().Unix() + tr.ExpiresIn
	}

	return TokenBundle{
		AccessToken:  tr.AccessToken,
		RefreshToken: newRefresh,
		ExpiresAt:    expiresAt,
		TokenType:    tr.TokenType,
	}, nil
}

// ExchangeAuthCode intercambia el código de autorización por un access token.
func ExchangeAuthCode(ctx context.Context, code, verifier, redirectURI string) (TokenBundle, error) {
	// Anthropic a menudo devuelve el formato "code#state" cuando se copia de la web
	if idx := strings.Index(code, "#"); idx != -1 {
		code = code[:idx]
	}
	code = strings.TrimSpace(code)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", ClientID)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenBundle{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := claudeHTTPClient().Do(req)
	if err != nil {
		return TokenBundle{}, fmt.Errorf("error al intercambiar código de Claude: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return TokenBundle{}, fmt.Errorf("respuesta de token no parseable (HTTP %d): %s", resp.StatusCode, string(body))
	}

	if tr.Error != "" {
		return TokenBundle{}, fmt.Errorf("error de autorización de Claude: %s (%s)", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return TokenBundle{}, fmt.Errorf("respuesta inesperada al intercambiar código (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var expiresAt int64
	if tr.ExpiresIn > 0 {
		expiresAt = time.Now().Unix() + tr.ExpiresIn
	}

	return TokenBundle{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresAt:    expiresAt,
		TokenType:    tr.TokenType,
	}, nil
}

// LoginPKCE orquesta el flujo de autenticación OAuth 2.0 PKCE para Claude.
// Soporta tanto captura automática en loopback (puerto 54321) como ingreso manual
// del código generado o del setup-token.
func LoginPKCE(ctx context.Context, onAuthURL func(string)) (TokenBundle, error) {
	// 1. Generar PKCE verifier
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return TokenBundle{}, fmt.Errorf("error generando entropía: %w", err)
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// 2. Generar PKCE challenge S256
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	// 3. Generar state
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return TokenBundle{}, fmt.Errorf("error generando state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	// 4. Iniciar listener HTTP local en puerto 54321 si está disponible
	redirectURI := LocalCallbackURI
	listener, err := net.Listen("tcp", "127.0.0.1:54321")
	useLocalServer := (err == nil)
	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	var server *http.Server
	if useLocalServer {
		defer listener.Close()
		mux := http.NewServeMux()
		mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
			query := r.URL.Query()
			if qErr := query.Get("error"); qErr != "" {
				errDesc := query.Get("error_description")
				http.Error(w, fmt.Sprintf("Error: %s (%s)", qErr, errDesc), http.StatusBadRequest)
				errChan <- fmt.Errorf("autorización rechazada: %s", qErr)
				return
			}
			code := query.Get("code")
			if code == "" {
				http.Error(w, "Falta código", http.StatusBadRequest)
				errChan <- errors.New("código ausente en callback")
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>MKD — Claude Autenticación Exitosa</title>
  <style>
    body { font-family: system-ui, sans-serif; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; background: #0f172a; color: #f8fafc; }
    .card { background: #1e293b; border: 1px solid #334155; border-radius: 12px; padding: 40px; text-align: center; max-width: 420px; box-shadow: 0 10px 25px rgba(0,0,0,0.5); }
    h1 { color: #d97706; font-size: 24px; margin-bottom: 12px; }
    p { color: #94a3b8; font-size: 15px; line-height: 1.5; margin-bottom: 0; }
  </style>
</head>
<body>
  <div class="card">
    <h1>✓ Sesión de Claude Iniciada en MKD</h1>
    <p>La autenticación con tu suscripción de Claude se completó exitosamente.<br><br>Ya puedes cerrar esta pestaña y volver a la terminal.</p>
  </div>
</body>
</html>`)
			codeChan <- code
		})

		server = &http.Server{Handler: mux}
		go func() {
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errChan <- err
			}
		}()
		defer server.Shutdown(context.Background())
	} else {
		// Si el puerto local no está libre, usar el callback web oficial de Anthropic
		redirectURI = RedirectURICallback
	}

	// 5. Construir URL de autorización
	authVals := url.Values{}
	authVals.Set("response_type", "code")
	authVals.Set("client_id", ClientID)
	authVals.Set("redirect_uri", redirectURI)
	authVals.Set("scope", Scope)
	authVals.Set("code_challenge", codeChallenge)
	authVals.Set("code_challenge_method", "S256")
	authVals.Set("state", state)

	finalAuthURL := AuthorizeURL + "?" + authVals.Encode()

	if onAuthURL != nil {
		onAuthURL(finalAuthURL)
	} else {
		openBrowser(finalAuthURL)
	}

	// 6. Si no hay servidor local, o como fallback interactivo para la terminal:
	// Esperamos el código vía callback HTTP o entrada de teclado si es un contexto interactivo.
	select {
	case code := <-codeChan:
		return ExchangeAuthCode(ctx, code, codeVerifier, redirectURI)
	case err := <-errChan:
		return TokenBundle{}, err
	case <-ctx.Done():
		return TokenBundle{}, ctx.Err()
	case <-time.After(500 * time.Millisecond):
		// Si aún no ha llegado el callback y estamos en un comando CLI interactivo,
		// ofrecer la posibilidad de pegar el código o setup-token directamente
		if !useLocalServer {
			fmt.Print("\nIngresa el código de autorización mostrado en el navegador (o presiona Enter para cancelar): ")
			reader := bufio.NewReader(os.Stdin)
			input, err := reader.ReadString('\n')
			if err != nil {
				return TokenBundle{}, err
			}
			input = strings.TrimSpace(input)
			if input == "" {
				return TokenBundle{}, errors.New("autenticación cancelada")
			}
			// Si el usuario ingresó un token directo (sk-ant-...)
			if strings.HasPrefix(input, "sk-ant-") {
				return TokenBundle{AccessToken: input}, nil
			}
			return ExchangeAuthCode(ctx, input, codeVerifier, redirectURI)
		}

		// Si el servidor local está activo, esperar por el canal HTTP o context
		select {
		case code := <-codeChan:
			return ExchangeAuthCode(ctx, code, codeVerifier, redirectURI)
		case err := <-errChan:
			return TokenBundle{}, err
		case <-ctx.Done():
			return TokenBundle{}, ctx.Err()
		}
	}
}

func openBrowser(targetURL string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", targetURL)
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	}
	if cmd != nil {
		_ = cmd.Start()
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
