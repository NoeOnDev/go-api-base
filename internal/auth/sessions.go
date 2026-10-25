package auth

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// sessionResponse es una sesión como la ve el usuario. El hash del token nunca sale de la API:
// se identifica con id. El user-agent va tal cual; el frontend puede mostrarlo legible
// ("Firefox en Windows") con una librería como ua-parser-js.
type sessionResponse struct {
	ID         int64     `json:"id"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	// Current indica la sesión de esta petición (este dispositivo).
	Current bool `json:"current"`
}

// listSessions responde GET /auth/sessions: las sesiones abiertas del usuario (sus dispositivos),
// de la más reciente a la más vieja.
func (s *Service) listSessions(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.q.ListUserSessions(r.Context(), MustUser(r.Context()).ID)
	if err != nil {
		return fmt.Errorf("listar sesiones: %w", err)
	}

	current := currentSessionID(r.Context())
	data := make([]sessionResponse, len(rows))
	for i, row := range rows {
		data[i] = sessionResponse{
			ID: row.ID, UserAgent: row.UserAgent, IP: row.IP,
			CreatedAt: row.CreatedAt, LastSeenAt: row.LastSeenAt,
			Current: row.ID == current,
		}
	}
	httpx.Write(w, http.StatusOK, map[string][]sessionResponse{"data": data})
	return nil
}

var errCurrentSession = httpx.Validation(map[string]string{"id": "es la sesión de este dispositivo; para cerrarla, usá POST /auth/logout"})

// revokeSession responde DELETE /auth/sessions/{id}: cierra una sesión del usuario, por ejemplo
// la de un dispositivo que no reconoce. La de otro usuario responde 404, como en notes. Se corta
// en el acto lo que dependía de ella (sus streams de GET /events).
func (s *Service) revokeSession(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	if id == currentSessionID(r.Context()) {
		return errCurrentSession
	}

	c, err := r.Cookie(s.cookieName(CookieName))
	if err != nil {
		return fmt.Errorf("leer la cookie de sesión: %w", err)
	}
	hash, err := s.q.DeleteUserSession(r.Context(), store.DeleteUserSessionParams{
		ID:        id,
		UserID:    MustUser(r.Context()).ID,
		TokenHash: hashToken(c.Value),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound("la sesión no existe")
	}
	if err != nil {
		return fmt.Errorf("cerrar sesión %d: %w", id, err)
	}
	if s.opts.SessionWatcher != nil {
		s.opts.SessionWatcher.SessionEnded(r.Context(), hex.EncodeToString(hash))
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// revokeOtherSessions responde POST /auth/sessions/revoke-others con {password}: cierra todas
// las sesiones del usuario salvo la actual. Pide la contraseña, como Laravel Jetstream, porque
// es una acción masiva: así alguien con una sesión robada no puede echar al dueño de todos sus
// dispositivos.
func (s *Service) revokeOtherSessions(w http.ResponseWriter, r *http.Request) error {
	var in passwordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	user := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), user.ID, in.Password, "password"); err != nil {
		return err
	}
	if err := s.endOtherSessions(r, user.ID); err != nil {
		return err
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
