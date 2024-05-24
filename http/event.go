package http

import (
	"errors"
	"net/http"

	"github.com/gorilla/mux"
)

// registerEventRoutes is a helper function to register event routes.
func (s *Server) registerEventRoutes(r *mux.Router) {
	r.HandleFunc("/events", s.handleEvents)
}

// handleEvents handles the "GET /events" route. This route provides real-time
// event notification over Websockets.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	LogError(r, errors.New("testing"))
}
