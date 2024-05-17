package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/benbjohnson/wtf"
	"github.com/benbjohnson/wtf/csv"
	"github.com/benbjohnson/wtf/http/html"
	"github.com/gorilla/mux"
)

// registerDialRoutes is a helper function for registering all dial routes.
func (s *Server) registerDialRoutes(r *mux.Router) {
	// Listing of all dials user is a member of.
	r.HandleFunc("/dials", s.handleDialIndex).Methods("GET")

	// API endpoint for creating dials.
	r.HandleFunc("/dials", s.handleDialCreate).Methods("POST")

	// HTML form for creating dials.
	r.HandleFunc("/dials/new", s.handleDialNew).Methods("GET")
	r.HandleFunc("/dials/new", s.handleDialCreate).Methods("POST")

	// View a single dial.
	r.HandleFunc("/dials/{id}", s.handleDialView).Methods("GET")

	// HTML form for updating an existing dial.
	r.HandleFunc("/dials/{id}/edit", s.handleDialEdit).Methods("GET")
	r.HandleFunc("/dials/{id}/edit", s.handleDialUpdate).Methods("PATCH")

	// Removing a dial.
	r.HandleFunc("/dials/{id}", s.handleDialDelete).Methods("DELETE")

	// Updating the value for the user's membership.
	r.HandleFunc("/dials/{id}/membership", s.handleDialSetMembershipValue).Methods("PUT")
}

// handleDialIndex handles the "GET /dials" route. This route can optionally
// accept filter arguments and outputs a list of all dials that the current
// user is a member of.
//
// The endpoint works with HTML, JSON, & CSV formats.
func (s *Server) handleDialIndex(w http.ResponseWriter, r *http.Request) {
	// Parse optional filter object.
	var filter wtf.DialFilter
	switch r.Header.Get("Content-type") {
	case "application/json":
		if err := json.NewDecoder(r.Body).Decode(&filter); err != nil {
			Error(w, r, wtf.Errorf(wtf.EINVALID, "Invalid JSON body"))
			return
		}
	default:
		filter.Offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
		filter.Limit = 20
	}

	// Fetch dials from database.
	dials, n, err := s.DialService.FindDials(r.Context(), filter)
	if err != nil {
		Error(w, r, err)
		return
	}

	// Render output based on HTTP accept header.
	switch r.Header.Get("Accept") {
	case "application/json":
		w.Header().Set("Content-type", "application/json")
		if err := json.NewEncoder(w).Encode(findDialsResponse{
			Dials: dials,
			N:     n,
		}); err != nil {
			LogError(r, err)
			return
		}

	case "text/csv":
		w.Header().Set("Content-type", "text/csv")
		enc := csv.NewDialEncoder(w)
		for _, dial := range dials {
			if err := enc.EncodeDial(dial); err != nil {
				LogError(r, err)
				return
			}
		}
		if err := enc.Close(); err != nil {
			LogError(r, err)
			return
		}

	default:
		tmpl := html.DialIndexTemplate{Dials: dials, N: n, Filter: filter, URL: *r.URL}
		tmpl.Render(r.Context(), w)
	}
}

// findDialsResponse represents the output JSON struct for "GET /dials".
type findDialsResponse struct {
	Dials []*wtf.Dial `json:"dials"`
	N     int         `json:"n"`
}

// handleDialView handles the "GET /dials/:id" route. It updates
func (s *Server) handleDialView(w http.ResponseWriter, r *http.Request) {
	// Parse ID from path.
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		Error(w, r, wtf.Errorf(wtf.EINVALID, "Invalid ID format"))
		return
	}

	// Fetch dial from the database.
	dial, err := s.DialService.FindDialByID(r.Context(), id)
	if err != nil {
		Error(w, r, err)
		return
	}

	// Fetch associated memberships from the database.
	dial.Memberships, _, err = s.DialMembershipService.FindDialMemberships(r.Context(), wtf.DialMembershipFilter{DialID: &dial.ID})
	if err != nil {
		Error(w, r, err)
		return
	}

	// Format returned data based on HTTP accept header.
	switch r.Header.Get("Accept") {
	case "application/json":
		w.Header().Set("Content-type", "application/json")
		if err := json.NewEncoder(w).Encode(dial); err != nil {
			LogError(r, err)
			return
		}

	default:
		tmpl := html.DialViewTemplate{
			Dial:      dial,
			InviteURL: fmt.Sprintf("%s/invite/%s", s.URL(), dial.InviteCode),
		}
		tmpl.Render(r.Context(), w)
	}
}

// handleDialNew handles the "GET /dials/new" route.
// It renders an HTML form for editing a new dial.
func (s *Server) handleDialNew(w http.ResponseWriter, r *http.Request) {
	tmpl := html.DialEditTemplate{Dial: &wtf.Dial{}}
	tmpl.Render(r.Context(), w)
}

// handleDialCreate handles the "POST /dials" and "POST /dials/new" route.
// It reads & writes data using with HTML or JSON.
func (s *Server) handleDialCreate(w http.ResponseWriter, r *http.Request) {
	// Unmarshal data based on HTTP request's content type.
	var dial wtf.Dial
	switch r.Header.Get("Content-type") {
	case "application/json":
		if err := json.NewDecoder(r.Body).Decode(&dial); err != nil {
			Error(w, r, wtf.Errorf(wtf.EINVALID, "Invalid JSON body"))
			return
		}
	default:
		dial.Name = r.PostFormValue("name")
	}

	// Create dial in the database.
	err := s.DialService.CreateDial(r.Context(), &dial)

	// Write new dial content to response based on accept header.
	switch r.Header.Get("Accept") {
	case "application/json":
		if err != nil {
			Error(w, r, err)
			return
		}

		w.Header().Set("Content-type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(dial); err != nil {
			LogError(r, err)
			return
		}

	default:
		// If we have an internal error, display the standard error page.
		// Otherwise it's probably a validation error so we can display the
		// error on the edit page with the user's dial data that was passed in.
		if wtf.ErrorCode(err) == wtf.EINTERNAL {
			Error(w, r, err)
			return
		} else if err != nil {
			tmpl := html.DialEditTemplate{Dial: &dial, Err: err}
			tmpl.Render(r.Context(), w)
			return
		}

		// Set a message to the user and redirect to the dial's new page.
		SetFlash(w, "Dial successfully created.")
		http.Redirect(w, r, fmt.Sprintf("/dials/%d", dial.ID), http.StatusFound)
	}
}

// handleDialEdit handles the "GET /dials/:id/edit" route. This route fetches
// the underlying dial and renders it in an HTML form.
func (s *Server) handleDialEdit(w http.ResponseWriter, r *http.Request) {
	// Parse dial ID from the path.
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		Error(w, r, wtf.Errorf(wtf.EINVALID, "Invalid ID format"))
		return
	}

	// Fetch dial from the database.
	dial, err := s.DialService.FindDialByID(r.Context(), id)
	if err != nil {
		Error(w, r, err)
		return
	}

	// Render dial in the HTML form.
	tmpl := html.DialEditTemplate{Dial: dial}
	tmpl.Render(r.Context(), w)
}

// handleDialUpdate handles the "PATCH /dials/:id/edit" route. This route
// reads in the updated fields and issues an update in the database. On success,
// it redirects to the dial's view page.
func (s *Server) handleDialUpdate(w http.ResponseWriter, r *http.Request) {
	// Parse dial ID from the path.
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		Error(w, r, wtf.Errorf(wtf.EINVALID, "Invalid ID format"))
		return
	}

	// Parse fields into an update object.
	var upd wtf.DialUpdate
	name := r.PostFormValue("name")
	upd.Name = &name

	// Update the dial in the database.
	dial, err := s.DialService.UpdateDial(r.Context(), id, upd)
	if wtf.ErrorCode(err) == wtf.EINTERNAL {
		Error(w, r, err)
		return
	} else if err != nil {
		tmpl := html.DialEditTemplate{Dial: dial, Err: err}
		tmpl.Render(r.Context(), w)
		return
	}

	// Save a message to display to the user on the next page.
	// Then redirect them to the dial's view page.
	SetFlash(w, "Dial successfully updated.")
	http.Redirect(w, r, fmt.Sprintf("/dials/%d", dial.ID), http.StatusFound)
}

// handleDialDelete handles the "DELETE /dials/:id" route. This route
// permanently deletes the dial and all its members and redirects to the
// dial listing page.
func (s *Server) handleDialDelete(w http.ResponseWriter, r *http.Request) {
	// Parse dial ID from path.
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		Error(w, r, wtf.Errorf(wtf.EINVALID, "Invalid ID format"))
		return
	}

	// Delete the dial from the database.
	if err := s.DialService.DeleteDial(r.Context(), id); err != nil {
		Error(w, r, err)
		return
	}

	// Render output to the client based on HTTP accept header.
	switch r.Header.Get("Accept") {
	case "application/json":
		w.Header().Set("Content-type", "application/json")
		w.Write([]byte(`{}`))

	default:
		SetFlash(w, "Dial successfully deleted.")
		http.Redirect(w, r, "/dials", http.StatusFound)
	}
}

// handleDialSetMembershipValue handles the "PUT /dials/:id/membership" route.
func (s *Server) handleDialSetMembershipValue(w http.ResponseWriter, r *http.Request) {
	var jsonRequest jsonSetDialMembershipValueRequest
	if err := json.NewDecoder(r.Body).Decode(&jsonRequest); err != nil {
		Error(w, r, wtf.Errorf(wtf.EINVALID, "Invalid JSON body"))
		return
	}

	// Parse dial ID from path.
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		Error(w, r, wtf.Errorf(wtf.EINVALID, "Invalid ID format"))
		return
	}

	// Update value for the user's membership on the dial.
	if err := s.DialService.SetDialMembershipValue(r.Context(), id, jsonRequest.Value); err != nil {
		Error(w, r, err)
		return
	}

	// Write response to indicate success.
	w.Header().Set("Content-type", "application/json")
	w.Write([]byte(`{}`))
}

type jsonSetDialMembershipValueRequest struct {
	Value int `json:"value"`
}
