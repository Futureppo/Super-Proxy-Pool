package web

import (
	"context"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"super-proxy-pool/internal/auth"
	"super-proxy-pool/internal/events"
	"super-proxy-pool/internal/models"
	"super-proxy-pool/internal/nodes"
	"super-proxy-pool/internal/pools"
	"super-proxy-pool/internal/probe"
	"super-proxy-pool/internal/settings"
	"super-proxy-pool/internal/subscriptions"
	webassets "super-proxy-pool/web"
)

type App struct {
	auth          *auth.Service
	settings      *settings.Service
	nodes         *nodes.Service
	subscriptions *subscriptions.Service
	pools         *pools.Service
	probe         *probe.Service
	events        *events.Broker
	loginTmpl     *template.Template
	appTmpl       *template.Template
}

type PageData struct {
	Title          string
	Page           string
	Heading        string
	Description    string
	SubscriptionID int64
}

type apiResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Message string      `json:"message,omitempty"`
}

type pageMeta struct {
	Page        string
	Heading     string
	Description string
}

func New(authSvc *auth.Service, settingsSvc *settings.Service, nodeSvc *nodes.Service, subSvc *subscriptions.Service, poolSvc *pools.Service, probeSvc *probe.Service, broker *events.Broker) (*App, error) {
	funcs := template.FuncMap{"eq": func(a, b string) bool { return a == b }}
	loginTmpl, err := template.New("login").Funcs(funcs).ParseFS(webassets.FS, "templates/base.html", "templates/login.html")
	if err != nil {
		return nil, err
	}
	appTmpl, err := template.New("app").Funcs(funcs).ParseFS(webassets.FS, "templates/base.html", "templates/app.html")
	if err != nil {
		return nil, err
	}
	return &App{
		auth:          authSvc,
		settings:      settingsSvc,
		nodes:         nodeSvc,
		subscriptions: subSvc,
		pools:         poolSvc,
		probe:         probeSvc,
		events:        broker,
		loginTmpl:     loginTmpl,
		appTmpl:       appTmpl,
	}, nil
}

func (a *App) Router() (http.Handler, error) {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	staticFS, err := fs.Sub(webassets.FS, "static")
	if err != nil {
		return nil, err
	}
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	r.Get("/", a.handleRoot)
	r.Get("/login", a.handleLoginPage)

	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/login", a.handleLogin)
		r.With(a.auth.RequireAuth).Post("/logout", a.handleLogout)
		r.With(a.auth.RequireAuth).Post("/change-password", a.handleChangePassword)
		r.With(a.auth.RequireAuth).Get("/me", a.handleMe)
	})

	r.Group(func(protected chi.Router) {
		protected.Use(a.auth.RequireAuth)

		protected.Get("/subscriptions", a.renderPage(pageMeta{
			Page:        "subscriptions",
			Heading:     "Subscriptions",
			Description: "Import and manage subscription sources.",
		}))
		protected.Get("/subscriptions/{id}", a.renderPage(pageMeta{
			Page:        "subscription-detail",
			Heading:     "Subscription Detail",
			Description: "View nodes, status and test actions for this subscription.",
		}))
		protected.Get("/manual-nodes", a.renderPage(pageMeta{
			Page:        "manual-nodes",
			Heading:     "Manual Nodes",
			Description: "Add raw nodes and manage their state.",
		}))
		protected.Get("/pools", a.renderPage(pageMeta{
			Page:        "pools",
			Heading:     "Proxy Pools",
			Description: "Create HTTP and SOCKS pools and choose members.",
		}))
		protected.Get("/settings", a.renderPage(pageMeta{
			Page:        "settings",
			Heading:     "System Settings",
			Description: "Edit panel, probe and runtime settings.",
		}))

		protected.Route("/api", func(api chi.Router) {
			api.Get("/events", a.handleEvents)

			api.Get("/subscriptions", a.handleSubscriptionList)
			api.Post("/subscriptions", a.handleSubscriptionCreate)
			api.Get("/subscriptions/{id}", a.handleSubscriptionGet)
			api.Put("/subscriptions/{id}", a.handleSubscriptionUpdate)
			api.Delete("/subscriptions/{id}", a.handleSubscriptionDelete)
			api.Post("/subscriptions/{id}/sync", a.handleSubscriptionSync)
			api.Get("/subscriptions/{id}/nodes", a.handleSubscriptionNodes)
			api.Post("/subscriptions/{id}/nodes/{nodeID}/latency-test", a.handleSubscriptionNodeLatency)
			api.Post("/subscriptions/{id}/nodes/{nodeID}/speed-test", a.handleSubscriptionNodeSpeed)
			api.Post("/subscriptions/{id}/nodes/{nodeID}/toggle", a.handleSubscriptionNodeToggle)

			api.Get("/manual-nodes", a.handleManualNodeList)
			api.Post("/manual-nodes", a.handleManualNodeCreate)
			api.Get("/manual-nodes/{id}", a.handleManualNodeGet)
			api.Put("/manual-nodes/{id}", a.handleManualNodeUpdate)
			api.Delete("/manual-nodes/{id}", a.handleManualNodeDelete)
			api.Post("/manual-nodes/{id}/latency-test", a.handleManualNodeLatency)
			api.Post("/manual-nodes/{id}/speed-test", a.handleManualNodeSpeed)
			api.Post("/manual-nodes/{id}/toggle", a.handleManualNodeToggle)

			api.Get("/pools/available-candidates", a.handlePoolCandidates)
			api.Get("/pools", a.handlePoolList)
			api.Post("/pools", a.handlePoolCreate)
			api.Get("/pools/{id}", a.handlePoolGet)
			api.Put("/pools/{id}", a.handlePoolUpdate)
			api.Delete("/pools/{id}", a.handlePoolDelete)
			api.Post("/pools/{id}/toggle", a.handlePoolToggle)
			api.Post("/pools/{id}/publish", a.handlePoolPublish)
			api.Get("/pools/{id}/members", a.handlePoolMembers)
			api.Put("/pools/{id}/members", a.handlePoolMembersUpdate)

			api.Get("/settings", a.handleSettingsGet)
			api.Put("/settings", a.handleSettingsUpdate)
			api.Post("/system/restart", a.handleRestart)
		})
	})

	return r, nil
}

func (a *App) handleRoot(w http.ResponseWriter, r *http.Request) {
	if a.auth.IsAuthenticated(r) {
		http.Redirect(w, r, "/subscriptions", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (a *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if a.auth.IsAuthenticated(r) {
		http.Redirect(w, r, "/subscriptions", http.StatusFound)
		return
	}
	_ = a.loginTmpl.ExecuteTemplate(w, "base", PageData{
		Title: "Login - Super-Proxy-Pool",
		Page:  "login",
	})
}

func (a *App) renderPage(meta pageMeta) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var subscriptionID int64
		if meta.Page == "subscription-detail" {
			subscriptionID = parseIDParam(r, "id")
		}
		_ = a.appTmpl.ExecuteTemplate(w, "base", PageData{
			Title:          meta.Heading + " - Super-Proxy-Pool",
			Page:           meta.Page,
			Heading:        meta.Heading,
			Description:    meta.Description,
			SubscriptionID: subscriptionID,
		})
	}
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	token, err := a.auth.Login(r.Context(), req.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Message: "invalid password"})
		return
	}
	a.auth.SetSessionCookie(w, token)
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"authenticated": true}})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		a.auth.Logout(cookie.Value)
	}
	a.auth.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"logged_out": true}})
}

func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := a.auth.ChangePassword(r.Context(), req.OldPassword, req.NewPassword); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Message: err.Error()})
		return
	}
	a.auth.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"changed": true}})
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"authenticated": true}})
}

func (a *App) handleSubscriptionList(w http.ResponseWriter, r *http.Request) {
	items, err := a.subscriptions.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	type subscriptionCard struct {
		models.Subscription
		TotalNodes       int    `json:"total_nodes"`
		AvailableNodes   int    `json:"available_nodes"`
		InvalidNodes     int    `json:"invalid_nodes"`
		AverageLatencyMS *int64 `json:"average_latency_ms"`
	}

	resp := make([]subscriptionCard, 0, len(items))
	for _, item := range items {
		nodes, err := a.subscriptions.ListNodes(r.Context(), item.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		card := subscriptionCard{Subscription: item, TotalNodes: len(nodes)}
		var latencySum int64
		var latencyCount int64
		for _, node := range nodes {
			if !node.Enabled || node.LastStatus == "unavailable" {
				card.InvalidNodes++
			}
			if node.LastStatus == "available" {
				card.AvailableNodes++
			}
			if node.LastLatencyMS != nil {
				latencySum += *node.LastLatencyMS
				latencyCount++
			}
		}
		if latencyCount > 0 {
			avg := latencySum / latencyCount
			card.AverageLatencyMS = &avg
		}
		resp = append(resp, card)
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: resp})
}

func (a *App) handleSubscriptionCreate(w http.ResponseWriter, r *http.Request) {
	var req subscriptions.UpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := a.subscriptions.Create(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, apiResponse{Success: true, Data: item})
}

func (a *App) handleSubscriptionGet(w http.ResponseWriter, r *http.Request) {
	item, err := a.subscriptions.Get(r.Context(), parseIDParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: item})
}

func (a *App) handleSubscriptionUpdate(w http.ResponseWriter, r *http.Request) {
	var req subscriptions.UpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := a.subscriptions.Update(r.Context(), parseIDParam(r, "id"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: item})
}

func (a *App) handleSubscriptionDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.subscriptions.Delete(r.Context(), parseIDParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	a.publishRuntimeAsync()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"deleted": true}})
}

func (a *App) handleSubscriptionSync(w http.ResponseWriter, r *http.Request) {
	item, err := a.subscriptions.Sync(r.Context(), parseIDParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	a.publishRuntimeAsync()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: item})
}

func (a *App) handleSubscriptionNodes(w http.ResponseWriter, r *http.Request) {
	items, err := a.subscriptions.ListNodes(r.Context(), parseIDParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: items})
}

func (a *App) handleSubscriptionNodeLatency(w http.ResponseWriter, r *http.Request) {
	if err := a.probe.EnqueueLatency("subscription", parseIDParam(r, "nodeID")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"queued": true}})
}

func (a *App) handleSubscriptionNodeSpeed(w http.ResponseWriter, r *http.Request) {
	if err := a.probe.EnqueueSpeed("subscription", parseIDParam(r, "nodeID")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"queued": true}})
}

func (a *App) handleSubscriptionNodeToggle(w http.ResponseWriter, r *http.Request) {
	item, err := a.subscriptions.ToggleNode(r.Context(), parseIDParam(r, "id"), parseIDParam(r, "nodeID"))
	if err != nil {
		writeError(w, err)
		return
	}
	a.publishRuntimeAsync()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: item})
}

func (a *App) handleManualNodeList(w http.ResponseWriter, r *http.Request) {
	items, err := a.nodes.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: items})
}

func (a *App) handleManualNodeCreate(w http.ResponseWriter, r *http.Request) {
	var req nodes.CreateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	items, parseErrs, err := a.nodes.Create(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, apiResponse{
		Success: true,
		Data: map[string]any{
			"items":        items,
			"parse_errors": stringifyErrors(parseErrs),
		},
	})
	a.publishRuntimeAsync()
}

func (a *App) handleManualNodeGet(w http.ResponseWriter, r *http.Request) {
	item, err := a.nodes.Get(r.Context(), parseIDParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: item})
}

func (a *App) handleManualNodeUpdate(w http.ResponseWriter, r *http.Request) {
	var req nodes.UpdateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := a.nodes.Update(r.Context(), parseIDParam(r, "id"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	a.publishRuntimeAsync()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: item})
}

func (a *App) handleManualNodeDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.nodes.Delete(r.Context(), parseIDParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	a.publishRuntimeAsync()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"deleted": true}})
}

func (a *App) handleManualNodeLatency(w http.ResponseWriter, r *http.Request) {
	if err := a.probe.EnqueueLatency("manual", parseIDParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"queued": true}})
}

func (a *App) handleManualNodeSpeed(w http.ResponseWriter, r *http.Request) {
	if err := a.probe.EnqueueSpeed("manual", parseIDParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"queued": true}})
}

func (a *App) handleManualNodeToggle(w http.ResponseWriter, r *http.Request) {
	item, err := a.nodes.Toggle(r.Context(), parseIDParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	a.publishRuntimeAsync()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: item})
}

func (a *App) handlePoolCandidates(w http.ResponseWriter, r *http.Request) {
	items, err := a.pools.AvailableCandidates(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: items})
}

func (a *App) handlePoolList(w http.ResponseWriter, r *http.Request) {
	items, err := a.pools.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: items})
}

func (a *App) handlePoolCreate(w http.ResponseWriter, r *http.Request) {
	var req pools.UpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := a.pools.Create(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	a.publishRuntimeAsync()
	writeJSON(w, http.StatusCreated, apiResponse{Success: true, Data: item})
}

func (a *App) handlePoolGet(w http.ResponseWriter, r *http.Request) {
	item, err := a.pools.Get(r.Context(), parseIDParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: item})
}

func (a *App) handlePoolUpdate(w http.ResponseWriter, r *http.Request) {
	var req pools.UpsertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := a.pools.Update(r.Context(), parseIDParam(r, "id"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	a.publishRuntimeAsync()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: item})
}

func (a *App) handlePoolDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.pools.Delete(r.Context(), parseIDParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	a.publishRuntimeAsync()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"deleted": true}})
}

func (a *App) handlePoolToggle(w http.ResponseWriter, r *http.Request) {
	item, err := a.pools.Toggle(r.Context(), parseIDParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	a.publishRuntimeAsync()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: item})
}

func (a *App) handlePoolPublish(w http.ResponseWriter, r *http.Request) {
	if err := a.pools.Publish(r.Context(), parseIDParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"published": true}})
}

func (a *App) handlePoolMembers(w http.ResponseWriter, r *http.Request) {
	members, err := a.pools.GetMembers(r.Context(), parseIDParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	candidates, err := a.pools.AvailableCandidates(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{
		"members":    members,
		"candidates": candidates,
	}})
}

func (a *App) handlePoolMembersUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Members []pools.MemberInput `json:"members"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := a.pools.UpdateMembers(r.Context(), parseIDParam(r, "id"), req.Members); err != nil {
		writeError(w, err)
		return
	}
	a.publishRuntimeAsync()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"updated": true}})
}

func (a *App) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	item, err := a.settings.Get(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: item})
}

func (a *App) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var req models.Settings
	if !decodeJSON(w, r, &req) {
		return
	}
	updated, restartRequired, err := a.settings.Update(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	message := "already applied"
	if restartRequired {
		message = "saved; restart required"
	} else {
		a.publishRuntimeAsync()
	}
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]any{
		"settings":      updated,
		"apply_message": message,
	}})
}

func (a *App) publishRuntimeAsync() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = a.pools.Publish(ctx, 0)
	}()
}

func (a *App) handleRestart(w http.ResponseWriter, r *http.Request) {
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: map[string]bool{"restarting": true}})
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	id, ch := a.events.Subscribe()
	defer a.events.Unsubscribe(id)

	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(data)
			_, _ = w.Write([]byte("\n\n"))
			flusher.Flush()
		}
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Message: "invalid json body"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload apiResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Message: err.Error()})
}

func parseIDParam(r *http.Request, key string) int64 {
	value, _ := strconv.ParseInt(chi.URLParam(r, key), 10, 64)
	return value
}

func stringifyErrors(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		out = append(out, err.Error())
	}
	return out
}
