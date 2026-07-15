package api

import (
	"net/http"
	"os"
)

// NewRouter creates the HTTP router with all API endpoints
func NewRouter(app *App, hub *SseHub) http.Handler {
	mux := http.NewServeMux()

	// SSE endpoint
	mux.HandleFunc("/events", hub.HandleSSE)

	// Auth
	mux.HandleFunc("POST /api/login", app.Login)

	// Users management
	mux.HandleFunc("GET /api/users", app.GetUsers)
	mux.HandleFunc("PUT /api/users", app.SaveUsers)

	// Config
	mux.HandleFunc("GET /api/config", app.GetConfig)
	mux.HandleFunc("PUT /api/config", app.SaveConfig)

	// API test & models
	mux.HandleFunc("POST /api/test-api", app.TestAPI)
	mux.HandleFunc("POST /api/fetch-models", app.FetchModels)
	mux.HandleFunc("GET /api/models", app.GetCachedModels)

	// Task management
	mux.HandleFunc("POST /api/task/start", app.StartTask)
	mux.HandleFunc("POST /api/task/stop", app.StopTask)
	mux.HandleFunc("GET /api/tasks", app.GetTasks)

	// Bank CRUD
	mux.HandleFunc("GET /api/bank/stats", app.GetBankStats)
	mux.HandleFunc("GET /api/bank/rows", app.GetBankRows)
	mux.HandleFunc("PUT /api/bank/entry", app.SaveBankEntry)
	mux.HandleFunc("DELETE /api/bank/entry/", app.DeleteBankEntry)
	mux.HandleFunc("POST /api/bank/batch-verify", app.BatchVerifyAll)
	mux.HandleFunc("POST /api/bank/batch-unverify", app.BatchUnverifyAll)

	// Static files - serve frontend
	frontendDir := "frontend"
	if d := os.Getenv("FRONTEND_DIR"); d != "" {
		frontendDir = d
	}
	fs := http.FileServer(http.Dir(frontendDir))
	mux.Handle("/", fs)

	// Apply middleware
	return Recovery(CORS(Logging(mux)))
}
