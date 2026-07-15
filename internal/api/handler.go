package api

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"examcoo-cloud/internal/core"
)

// Task represents a running exam task
type Task struct {
	ID     string
	Cancel context.CancelFunc
	Status string // "running" / "finished" / "error"
	UserID string // employee_id
}

// App is the HTTP API handler
type App struct {
	mu           sync.Mutex
	tasks        map[string]*Task
	cachedModels []string
	hub          *SseHub
}

// NewApp creates a new App instance
func NewApp(hub *SseHub) *App {
	return &App{
		tasks: make(map[string]*Task),
		hub:   hub,
	}
}

// getEmployeeID extracts employee_id from request header
func getEmployeeID(r *http.Request) string {
	return r.Header.Get("X-Employee-ID")
}

// generateToken generates a simple token
func generateToken(employeeID, name string) string {
	h := md5.Sum([]byte(employeeID + ":" + name + ":examcoo"))
	return fmt.Sprintf("%x", h)
}

// emit creates an Emit callback that broadcasts via SSE
func (a *App) emit(taskID string) core.Emit {
	return func(text, level string) {
		a.hub.Broadcast(taskID, SseMessage{
			Type: "log",
			Data: map[string]string{"text": text, "level": level},
		})
	}
}

// sendTaskState broadcasts task state change
func (a *App) sendTaskState(taskID string, running bool) {
	a.hub.Broadcast(taskID, SseMessage{
		Type: "task-state",
		Data: map[string]bool{"running": running},
	})
}

// sendBankStats broadcasts bank stats to all clients
func (a *App) sendBankStats() {
	total, verified := core.BankStats()
	a.hub.BroadcastAll(SseMessage{
		Type: "bank-stats",
		Data: map[string]int{"total": total, "verified": verified},
	})
}

// respond writes a JSON response
func respond(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": msg})
}

func respondOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if data == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "data": data})
	}
}

// ── POST /api/login ──

func (a *App) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EmployeeID string `json:"employee_id"`
		Name       string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body")
		return
	}
	if req.EmployeeID == "" || req.Name == "" {
		respondError(w, "请填写员工号和姓名")
		return
	}

	// Validate user against whitelist
	valid, user := core.ValidateUser(req.EmployeeID, req.Name)
	if !valid {
		respondError(w, "登录失败: 员工号或姓名错误")
		return
	}

	// Ensure user directory exists
	if err := core.EnsureUserDir(req.EmployeeID); err != nil {
		respondError(w, "创建用户目录失败: "+err.Error())
		return
	}

	token := generateToken(req.EmployeeID, req.Name)
	respondOK(w, map[string]interface{}{
		"token":       token,
		"employee_id": user.EmployeeID,
		"name":        user.Name,
		"department":  user.Department,
		"is_admin":    user.IsAdmin,
	})
}

// ── GET /api/config ──

func (a *App) GetConfig(w http.ResponseWriter, r *http.Request) {
	employeeID := getEmployeeID(r)
	if employeeID == "" {
		respondError(w, "未登录")
		return
	}
	cfg := core.LoadUserConfig(employeeID)
	// Mask API key for security
	if len(cfg.APIKey) > 4 {
		cfg.APIKey = cfg.APIKey[:4] + "****"
	}
	respond(w, cfg)
}

// ── PUT /api/config ──

func (a *App) SaveConfig(w http.ResponseWriter, r *http.Request) {
	employeeID := getEmployeeID(r)
	if employeeID == "" {
		respondError(w, "未登录")
		return
	}
	var cfg core.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		respondError(w, "invalid request body")
		return
	}

	// If API key is masked (contains ****), keep the original value
	if strings.Contains(cfg.APIKey, "****") {
		oldCfg := core.LoadUserConfig(employeeID)
		cfg.APIKey = oldCfg.APIKey
		log.Printf("Config saved for %s: API Key kept original (masked)", employeeID)
	} else {
		log.Printf("Config saved for %s: API Key length=%d", employeeID, len(cfg.APIKey))
	}

	if err := core.SaveUserConfig(employeeID, cfg); err != nil {
		respondError(w, "save failed: "+err.Error())
		return
	}
	respondOK(w, nil)
}

// ── POST /api/test-api ──

func (a *App) TestAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		Model    string `json:"model"`
		ProxyURL string `json:"proxy_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body")
		return
	}
	latency, err := core.TestAPIKey(req.BaseURL, req.APIKey, req.Model, req.ProxyURL)
	if err != nil {
		respondError(w, err.Error())
		return
	}
	respondOK(w, map[string]interface{}{"latency": latency.Milliseconds()})
}

// ── POST /api/fetch-models ──

func (a *App) FetchModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		ProxyURL string `json:"proxy_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body")
		return
	}
	models, err := core.FetchModels(req.BaseURL, req.APIKey, req.ProxyURL)
	if err != nil {
		respondError(w, err.Error())
		return
	}
	a.mu.Lock()
	a.cachedModels = models
	a.mu.Unlock()
	respondOK(w, models)
}

// ── GET /api/models ──

func (a *App) GetCachedModels(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	respond(w, a.cachedModels)
}

// ── POST /api/task/start ──

func (a *App) StartTask(w http.ResponseWriter, r *http.Request) {
	employeeID := getEmployeeID(r)
	if employeeID == "" {
		respondError(w, "未登录")
		return
	}

	var req struct {
		ExamURL string      `json:"exam_url"`
		Config  core.Config `json:"config"`
		Mode    string      `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body")
		return
	}

	if req.ExamURL == "" {
		respondError(w, "请输入考试链接！")
		return
	}
	if req.Config.Name == "" || req.Config.EmployeeID == "" || req.Config.Department == "" {
		respondError(w, "请填写姓名、工号、部门！")
		return
	}
	if req.Config.BaseURL == "" {
		respondError(w, "请填写 AI Base URL！")
		return
	}

	// Save user config
	_ = core.SaveUserConfig(employeeID, req.Config)

	// Generate task ID
	taskID := fmt.Sprintf("%d", time.Now().UnixNano())

	ctx, cancel := context.WithCancel(context.Background())
	task := &Task{ID: taskID, Cancel: cancel, Status: "running", UserID: employeeID}
	a.mu.Lock()
	a.tasks[taskID] = task
	a.mu.Unlock()

	a.sendTaskState(taskID, true)

	go func() {
		// Wait for client to connect SSE
		time.Sleep(500 * time.Millisecond)
		emit := a.emit(taskID)
		if req.Mode == "answer" {
			core.RunAutoExam(ctx, req.ExamURL, req.Config, emit)
		} else {
			core.RunBuildBank(ctx, req.ExamURL, req.Config, emit)
		}
		a.mu.Lock()
		task.Status = "finished"
		delete(a.tasks, taskID)
		a.mu.Unlock()
		a.sendTaskState(taskID, false)
		a.sendBankStats()
	}()

	respondOK(w, map[string]string{"task_id": taskID})
}

// ── POST /api/task/stop ──

func (a *App) StopTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body")
		return
	}

	a.mu.Lock()
	task, ok := a.tasks[req.TaskID]
	a.mu.Unlock()

	if !ok {
		respondError(w, "task not found")
		return
	}
	task.Cancel()
	respondOK(w, nil)
}

// ── GET /api/tasks ──

func (a *App) GetTasks(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var taskList []map[string]string
	for _, t := range a.tasks {
		taskList = append(taskList, map[string]string{
			"id":     t.ID,
			"status": t.Status,
			"user_id": t.UserID,
		})
	}
	respond(w, taskList)
}

// ── GET /api/bank/stats ──

func (a *App) GetBankStats(w http.ResponseWriter, r *http.Request) {
	total, verified := core.BankStats()
	respond(w, map[string]int{"total": total, "verified": verified})
}

// ── GET /api/bank/rows ──

func (a *App) GetBankRows(w http.ResponseWriter, r *http.Request) {
	rows := core.BankRows()
	respond(w, rows)
}

// ── PUT /api/bank/entry ──

func (a *App) SaveBankEntry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key      string `json:"key"`
		Answer   string `json:"answer"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, "invalid request body")
		return
	}

	bank := core.LoadBankForUpdate()
	entry, ok := bank[req.Key]
	if !ok {
		respondError(w, "题目不存在")
		return
	}
	entry.Answer = req.Answer
	entry.Verified = req.Verified
	bank[req.Key] = entry
	if err := core.SaveBankForUpdate(bank); err != nil {
		respondError(w, "保存失败: "+err.Error())
		return
	}
	a.sendBankStats()
	respondOK(w, nil)
}

// ── DELETE /api/bank/entry/{key} ──

func (a *App) DeleteBankEntry(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/api/bank/entry/")
	if key == "" {
		respondError(w, "missing key")
		return
	}

	bank := core.LoadBankForUpdate()
	if _, ok := bank[key]; !ok {
		respondError(w, "题目不存在")
		return
	}
	delete(bank, key)
	if err := core.SaveBankForUpdate(bank); err != nil {
		respondError(w, err.Error())
		return
	}
	a.sendBankStats()
	respondOK(w, map[string]string{"message": "已删除，剩余 " + strconv.Itoa(len(bank)) + " 题"})
}

// ── POST /api/bank/batch-verify ──

func (a *App) BatchVerifyAll(w http.ResponseWriter, r *http.Request) {
	bank := core.LoadBankForUpdate()
	for k, e := range bank {
		e.Verified = true
		bank[k] = e
	}
	if err := core.SaveBankForUpdate(bank); err != nil {
		respondError(w, err.Error())
		return
	}
	a.sendBankStats()
	respondOK(w, map[string]string{"message": "已将全部 " + strconv.Itoa(len(bank)) + " 题标记为已校验"})
}

// ── POST /api/bank/batch-unverify ──

func (a *App) BatchUnverifyAll(w http.ResponseWriter, r *http.Request) {
	bank := core.LoadBankForUpdate()
	for k, e := range bank {
		e.Verified = false
		bank[k] = e
	}
	if err := core.SaveBankForUpdate(bank); err != nil {
		respondError(w, err.Error())
		return
	}
	a.sendBankStats()
	respondOK(w, map[string]string{"message": "已清除全部校验状态"})
}

// ── GET /api/users ──

func (a *App) GetUsers(w http.ResponseWriter, r *http.Request) {
	users := core.LoadUsers()
	respond(w, users)
}

// ── PUT /api/users ──

func (a *App) SaveUsers(w http.ResponseWriter, r *http.Request) {
	employeeID := getEmployeeID(r)
	if employeeID == "" {
		respondError(w, "未登录")
		return
	}
	if !core.IsAdmin(employeeID) {
		respondError(w, "权限不足: 只有管理员可以维护用户名单")
		return
	}

	var users []core.UserEntry
	if err := json.NewDecoder(r.Body).Decode(&users); err != nil {
		respondError(w, "invalid request body")
		return
	}
	if err := core.SaveUsers(users); err != nil {
		respondError(w, "保存失败: "+err.Error())
		return
	}
	respondOK(w, nil)
}

// ── GET /api/debug/storage ──

func (a *App) DebugStorage(w http.ResponseWriter, r *http.Request) {
	employeeID := getEmployeeID(r)
	if employeeID == "" {
		respondError(w, "未登录")
		return
	}
	if !core.IsAdmin(employeeID) {
		respondError(w, "权限不足")
		return
	}

	result := map[string]interface{}{
		"use_redis": core.UseRedis(),
	}

	if core.UseRedis() {
		// List all config keys
		configs := core.ListRedisConfigs()
		result["configs"] = configs

		// Get users list
		users := core.LoadUsers()
		result["users"] = users

		// Get bank stats
		total, verified := core.BankStats()
		result["bank_stats"] = map[string]int{"total": total, "verified": verified}
	} else {
		result["message"] = "使用文件存储，无 Redis 数据"
	}

	respond(w, result)
}
