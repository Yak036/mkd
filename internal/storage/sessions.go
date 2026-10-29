package storage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session es una conversación persistida en disco.
type Session struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Cwd      string    `json:"cwd"`
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Files    []string  `json:"files,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	Messages []Message `json:"messages"`
	// Swarm conserva el estado completo de la misión Swarm (pestañas, outputs, pasos, orden).
	Swarm *SwarmSessionState `json:"swarm,omitempty"`
	// SwarmTabs y SwarmTab conservan retrocompatibilidad con versiones previas.
	SwarmTabs []string  `json:"swarm_tabs,omitempty"`
	SwarmTab  int       `json:"swarm_tab,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SwarmSessionState representa el estado global persistido de una misión Swarm.
type SwarmSessionState struct {
	Mission            string                     `json:"mission,omitempty"`
	OrchestratorStatus string                     `json:"orchestrator_status,omitempty"`
	Delegations        int                        `json:"delegations,omitempty"`
	Order              []string                   `json:"order,omitempty"`
	SelectedTab        int                        `json:"selected_tab,omitempty"`
	Running            bool                       `json:"running,omitempty"`
	Agents             map[string]SwarmTabSession `json:"agents,omitempty"`
}

// SwarmTabSession representa el estado persistido de una pestaña de sub-agente.
type SwarmTabSession struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Icon         string             `json:"icon,omitempty"`
	Color        string             `json:"color,omitempty"`
	Task         string             `json:"task,omitempty"`
	Instructions string             `json:"instructions,omitempty"`
	Status       string             `json:"status"` // "queued", "running", "waiting_approval", "done", "error"
	Tool         string             `json:"tool,omitempty"`
	Args         string             `json:"args,omitempty"`
	LastLine     string             `json:"last_line,omitempty"`
	Output       string             `json:"output,omitempty"`
	Tokens       int                `json:"tokens,omitempty"`
	DurationMs   int64              `json:"duration_ms,omitempty"`
	Index        int                `json:"index"`
	Err          string             `json:"err,omitempty"`
	Steps        []SwarmStepSession `json:"steps,omitempty"`
}

// SwarmStepSession guarda una acción/tool ejecutada por un sub-agente.
type SwarmStepSession struct {
	Tool  string `json:"tool"`
	Args  string `json:"args,omitempty"`
	Done  bool   `json:"done"`
	Error bool   `json:"error,omitempty"`
	Text  string `json:"text,omitempty"`
}

// Message es un mensaje persistido. Storage no depende de otros paquetes.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Display string `json:"display,omitempty"`
}

// NewSessionID genera un identificador único basado en tiempo + aleatorio.
func NewSessionID() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("%s-%s", time.Now().Format("20060102-150405"), hex.EncodeToString(buf[:]))
}

// SessionPath devuelve la ruta del archivo de una sesión.
func SessionPath(id string) (string, error) {
	id = sanitizeID(id)
	if id == "" {
		return "", errors.New("session id vacío")
	}
	dir, err := sessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".json"), nil
}

// SaveSession escribe la sesión de forma atómica, fijando UpdatedAt.
func SaveSession(session Session) (string, error) {
	if strings.TrimSpace(session.ID) == "" {
		session.ID = NewSessionID()
	}
	session.ID = sanitizeID(session.ID)
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now()
	}
	session.UpdatedAt = time.Now()

	path, err := SessionPath(session.ID)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := writeAtomic(path, data); err != nil {
		return "", err
	}
	return path, nil
}

// LoadSession lee una sesión por su ID.
func LoadSession(id string) (Session, error) {
	path, err := SessionPath(id)
	if err != nil {
		return Session{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Session{}, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return Session{}, fmt.Errorf("sesión corrupta %s: %w", path, err)
	}
	if session.ID == "" {
		session.ID = sanitizeID(id)
	}
	return session, nil
}

// ListSessions devuelve las sesiones ordenadas por UpdatedAt descendente.
func ListSessions() ([]Session, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	sessions := make([]Session, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		session, err := LoadSession(id)
		if err != nil {
			continue
		}
		sessions = append(sessions, session)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

// DeleteSession elimina el archivo de una sesión.
func DeleteSession(id string) error {
	path, err := SessionPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// DeriveTitle genera un título corto a partir del primer prompt del usuario.
func DeriveTitle(prompt string) string {
	line := strings.TrimSpace(prompt)
	if line == "" {
		return "Sesión sin título"
	}
	line = strings.TrimSpace(stripTags(line))
	line = strings.TrimSpace(strings.SplitN(line, "\n", 2)[0])
	line = strings.TrimSpace(strings.SplitN(line, "\r", 2)[0])
	line = strings.Join(strings.Fields(line), " ")
	if line == "" {
		return "Sesión sin título"
	}
	const limit = 48
	runes := []rune(line)
	if len(runes) > limit {
		line = strings.TrimSpace(string(runes[:limit])) + "…"
	}
	return line
}

func sanitizeID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimSuffix(id, ".json")
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stripTags elimina los bloques de contexto (<context_files>…</context_files>)
// que el ensamblador añade a los mensajes enviados al modelo.
func stripTags(value string) string {
	pairs := [][2]string{
		{"<context_files>", "</context_files>"},
		{"<context_summary>", "</context_summary>"},
		{"<file ", "</file>"},
	}
	for _, pair := range pairs {
		for {
			start := strings.Index(value, pair[0])
			if start < 0 {
				break
			}
			end := strings.Index(value[start:], pair[1])
			if end < 0 {
				value = value[:start]
				break
			}
			value = value[:start] + " " + value[start+end+len(pair[1]):]
		}
	}
	return value
}
