package auth

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// register responde POST /auth/register: crea un usuario con rol user e inicia su sesión.
func (s *Service) register(w http.ResponseWriter, r *http.Request) error {
	var in RegisterInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	u, err := s.CreateUser(r.Context(), in, RoleUser)
	if err != nil {
		return err
	}

	if err := s.StartSession(r.Context(), w, u.ID); err != nil {
		return err
	}

	httpx.Write(w, http.StatusCreated, u)
	return nil
}

// login responde POST /auth/login.
func (s *Service) login(w http.ResponseWriter, r *http.Request) error {
	var in loginInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}

	u, err := s.Authenticate(r.Context(), in.Email, in.Password)
	if err != nil {
		return err
	}

	if err := s.StartSession(r.Context(), w, u.ID); err != nil {
		return err
	}

	httpx.Write(w, http.StatusOK, u)
	return nil
}

// logout responde POST /auth/logout.
func (s *Service) logout(w http.ResponseWriter, r *http.Request) error {
	if err := s.EndSession(r.Context(), w, r); err != nil {
		return err
	}
	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// me responde GET /auth/me con el usuario de la sesión.
func (s *Service) me(w http.ResponseWriter, r *http.Request) error {
	httpx.Write(w, http.StatusOK, MustUser(r.Context()))
	return nil
}
