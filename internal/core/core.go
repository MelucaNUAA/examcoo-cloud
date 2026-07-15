package core

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const AppVersion = "v1.0"
const AppTitle = "ExamCoo 自动答题工具"

// ─────────────────────────────────────────────────────────────
// 数据类型
// ─────────────────────────────────────────────────────────────

type Config struct {
	Name         string  `json:"name"`
	EmployeeID   string  `json:"employee_id"`
	Department   string  `json:"department"`
	APIKey       string  `json:"api_key"`
	BaseURL      string  `json:"base_url"`
	Model        string  `json:"model"`
	ProxyURL     string  `json:"proxy_url"`
	VoteRounds   int     `json:"vote_rounds"`
	LoopCount    int     `json:"loop_count"`
	RequestDelay float64 `json:"request_delay"`
	SubmitDelay  float64 `json:"submit_delay"`
	TargetScore  float64 `json:"target_score"`
}

var DefaultConfig = Config{
	APIKey:       "anonymous",
	BaseURL:      "https://api.kilo.ai/api/openrouter",
	Model:        "bytedance-seed/dola-seed-2.0-pro:free",
	VoteRounds:   3,
	LoopCount:    1,
	RequestDelay: 1.0,
	SubmitDelay:  3.0,
	TargetScore:  100,
}

type Question struct {
	ID      string          `json:"id"`
	Text    string          `json:"a"`
	Options json.RawMessage `json:"b"`
	Score   json.RawMessage `json:"d"`
}

// parseQuestionScore 健壮解析题目分值，支持整数/字符串/"null"/缺失
func parseQuestionScore(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	s := strings.Trim(strings.TrimSpace(string(raw)), "\"")
	if s == "" || s == "null" {
		return 0
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
		return int(f)
	}
	return 0
}

// calcTotalScore 求所有题目分值之和；hasReal=true 表示题目 JSON 中有真实分值
func calcTotalScore(questions []Question) (total int, hasReal bool) {
	for _, q := range questions {
		total += parseQuestionScore(q.Score)
	}
	hasReal = total > 0
	if !hasReal {
		total = len(questions) * 5 // 兜底估算
	}
	return
}

type Option struct {
	Text string `json:"o"`
}

type BankEntry struct {
	Answer     string            `json:"answer"`
	Verified   bool              `json:"verified"`
	Text       string            `json:"text"`
	QType      string            `json:"q_type"`
	OptionsMap map[string]string `json:"options_map"`
}

type BankRow struct {
	Key        string
	Answer     string
	Verified   bool
	Text       string
	QType      string
	OptionsMap map[string]string // A->选项文本, B->..., 判断题为空
}

// ─────────────────────────────────────────────────────────────
// 文件路径 & 配置
// ─────────────────────────────────────────────────────────────

// dataDir returns the data directory for config and bank files.
// Uses DATA_DIR env var if set, otherwise falls back to working directory.
func dataDir() string {
	if d := os.Getenv("DATA_DIR"); d != "" {
		return d
	}
	return "."
}

// userDir returns the user-specific directory
func userDir(employeeID string) string {
	return filepath.Join(dataDir(), "users", employeeID)
}

func configPath() string { return filepath.Join(dataDir(), "config.json") }
func bankPath() string   { return filepath.Join(dataDir(), "answer_bank.json") }

func UserConfigPath(employeeID string) string {
	return filepath.Join(userDir(employeeID), "config.json")
}

// EnsureUserDir creates user directory if not exists
func EnsureUserDir(employeeID string) error {
	return os.MkdirAll(userDir(employeeID), 0755)
}

// LoadUserConfig loads user-specific config
func LoadUserConfig(employeeID string) Config {
	if UseRedis() {
		cfg := LoadUserConfigFromRedis(employeeID)
		applyConfigDefaults(&cfg)
		return cfg
	}

	cfg := DefaultConfig
	data, err := os.ReadFile(UserConfigPath(employeeID))
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	applyConfigDefaults(&cfg)
	return cfg
}

// SaveUserConfig saves user-specific config
func SaveUserConfig(employeeID string, cfg Config) error {
	if UseRedis() {
		return SaveUserConfigToRedis(employeeID, cfg, false)
	}

	if err := EnsureUserDir(employeeID); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(UserConfigPath(employeeID), data, 0644)
}

// SaveUserConfigRaw saves config without encrypting API Key
func SaveUserConfigRaw(employeeID string, cfg Config) error {
	if UseRedis() {
		return SaveUserConfigToRedis(employeeID, cfg, true)
	}

	if err := EnsureUserDir(employeeID); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(UserConfigPath(employeeID), data, 0644)
}

// applyConfigDefaults applies default values to config
func applyConfigDefaults(cfg *Config) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultConfig.BaseURL
	}
	if cfg.RequestDelay == 0 {
		cfg.RequestDelay = 1.0
	}
	if cfg.VoteRounds == 0 {
		cfg.VoteRounds = 3
	}
	if cfg.LoopCount == 0 {
		cfg.LoopCount = 1
	}
	if cfg.SubmitDelay == 0 {
		cfg.SubmitDelay = 3.0
	}
	if cfg.TargetScore == 0 {
		cfg.TargetScore = 100
	}
}

func LoadConfig() Config {
	cfg := DefaultConfig
	data, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultConfig.BaseURL
	}
	if cfg.RequestDelay == 0 {
		cfg.RequestDelay = 1.0
	}
	if cfg.VoteRounds == 0 {
		cfg.VoteRounds = 3
	}
	if cfg.LoopCount == 0 {
		cfg.LoopCount = 1
	}
	if cfg.SubmitDelay == 0 {
		cfg.SubmitDelay = 3.0
	}
	if cfg.TargetScore == 0 {
		cfg.TargetScore = 100
	}
	return cfg
}

func SaveConfig(cfg Config) error {
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(configPath(), data, 0644)
}

// ─────────────────────────────────────────────────────────────
// 题库
// ─────────────────────────────────────────────────────────────

// bankMu protects concurrent access to answer_bank.json
var bankMu sync.RWMutex

// questionKey returns the stable key for bank storage.
// Uses the API-provided question ID (e.g. "s1_1336888777351761923") directly
// so the same question is always matched regardless of text/option changes.
func questionKey(q Question) string {
	if q.ID != "" {
		return q.ID
	}
	// Fallback for legacy entries without ID
	opts := parseOptions(q.Options)
	parts := make([]string, len(opts))
	for i, o := range opts {
		parts[i] = o.Text
	}
	raw := q.Text + "||" + strings.Join(parts, "|")
	return fmt.Sprintf("%x", md5.Sum([]byte(raw)))
}

func loadBank() map[string]BankEntry {
	bankMu.RLock()
	defer bankMu.RUnlock()
	return loadBankUnlocked()
}

func loadBankUnlocked() map[string]BankEntry {
	if UseRedis() {
		return LoadBankFromRedis()
	}

	bank := map[string]BankEntry{}
	data, err := os.ReadFile(bankPath())
	if err != nil {
		return bank
	}
	_ = json.Unmarshal(data, &bank)
	return bank
}

func saveBank(bank map[string]BankEntry) error {
	bankMu.Lock()
	defer bankMu.Unlock()
	return saveBankUnlocked(bank)
}

func saveBankUnlocked(bank map[string]BankEntry) error {
	if UseRedis() {
		return SaveBankToRedis(bank)
	}

	data, _ := json.MarshalIndent(bank, "", "  ")
	return os.WriteFile(bankPath(), data, 0644)
}

// LoadBankForUpdate loads bank with write lock held. Caller must call SaveBankForUpdate when done.
func LoadBankForUpdate() map[string]BankEntry {
	bankMu.Lock()
	return loadBankUnlocked()
}

// SaveBankForUpdate saves bank and releases write lock.
func SaveBankForUpdate(bank map[string]BankEntry) error {
	defer bankMu.Unlock()
	return saveBankUnlocked(bank)
}

func BankStats() (total, verified int) {
	bank := loadBank()
	for _, v := range bank {
		total++
		if v.Verified {
			verified++
		}
	}
	return
}

func BankRows() []BankRow {
	bank := loadBank()
	rows := make([]BankRow, 0, len(bank))
	for k, v := range bank {
		rows = append(rows, BankRow{
			Key:        k,
			Answer:     v.Answer,
			Verified:   v.Verified,
			Text:       v.Text,
			QType:      v.QType,
			OptionsMap: v.OptionsMap,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Verified != rows[j].Verified {
			return rows[i].Verified
		}
		return rows[i].Text < rows[j].Text
	})
	return rows
}

// ─────────────────────────────────────────────────────────────
// 工具
// ─────────────────────────────────────────────────────────────

func parseOptions(raw json.RawMessage) []Option {
	if len(raw) == 0 {
		return nil
	}
	var opts []Option
	if err := json.Unmarshal(raw, &opts); err == nil {
		return opts
	}
	// "b" 可能是双重编码的 JSON 字符串（外层引号包裹内层数组）
	var inner string
	if err := json.Unmarshal(raw, &inner); err == nil && inner != "" {
		_ = json.Unmarshal([]byte(inner), &opts)
	}
	return opts
}

func qType(q Question) string {
	if i := strings.IndexByte(q.ID, '_'); i > 0 {
		return q.ID[:i]
	}
	return q.ID
}

func typeLabel(qt string) string {
	switch qt {
	case "s1":
		return "单选"
	case "s2":
		return "多选"
	case "s3":
		return "判断"
	}
	return "?"
}

func encodeAnswer(answer, qt string) string {
	answer = strings.ToUpper(strings.TrimSpace(answer))
	if qt == "s3" {
		if answer == "正确" || answer == "正" || answer == "对" ||
			answer == "TRUE" || answer == "T" || answer == "1" {
			return "1"
		}
		return "2"
	}
	val := 0
	for _, c := range answer {
		if v, ok := optionMap[string(c)]; ok {
			val += v
		}
	}
	if val == 0 {
		return "1"
	}
	return strconv.Itoa(val)
}

var optionMap = map[string]int{"A": 1, "B": 2, "C": 4, "D": 8}

func truncRune(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func sleepDelay(cfg Config) {
	if cfg.RequestDelay > 0 {
		time.Sleep(time.Duration(cfg.RequestDelay*1000) * time.Millisecond)
	}
}

func sleepSubmitDelay(start time.Time, cfg Config) {
	elapsed := time.Since(start).Seconds()
	remain := cfg.SubmitDelay - elapsed
	if remain > 0 {
		time.Sleep(time.Duration(remain*1000) * time.Millisecond)
	}
}

func newHTTPClient(timeout time.Duration, proxyURL string) *http.Client {
	c := &http.Client{Timeout: timeout}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			c.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
		}
	}
	return c
}

func makeClient(proxyURL string) *http.Client {
	return newHTTPClient(30*time.Second, proxyURL)
}

func ts() string { return time.Now().Format("15:04:05") }

// ─────────────────────────────────────────────────────────────
// 网络请求
// ─────────────────────────────────────────────────────────────

var ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"

func fetchPage(client *http.Client, url string) (string, error) {
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	req, err := http.NewRequest("GET",
		url+sep+"_t="+strconv.FormatInt(time.Now().UnixMilli(), 10), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func extractIDs(html string) (leid, token string, err error) {
	for _, p := range []string{
		`"leid"\s*:\s*"?(\d+)"?`,
		`leid\s*[:=]\s*['"]?(\d+)['"]?`,
		`/leid/(\d+)/tokenleid/`,
		`leid=(\d+)`,
	} {
		if m := regexp.MustCompile(p).FindStringSubmatch(html); m != nil {
			leid = m[1]
			break
		}
	}
	for _, p := range []string{
		`"tokenleid"\s*:\s*"([0-9a-f]+)"`,
		`tokenleid\s*[:=]\s*['"]([0-9a-f]+)['"]`,
		`/tokenleid/([0-9a-f]+)`,
		`tokenleid=([0-9a-f]+)`,
	} {
		if m := regexp.MustCompile(p).FindStringSubmatch(html); m != nil {
			token = m[1]
			break
		}
	}
	if leid == "" || token == "" {
		err = fmt.Errorf("无法提取 leid / tokenleid，请检查链接")
	}
	return
}

func fetchQuestions(client *http.Client, leid, token, referer string) ([]Question, error) {
	url := fmt.Sprintf(
		"https://www.examcoo.com/editor/rpc/getreexamcontent/leid/%s/tokenleid/%s",
		leid, token)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", referer)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Questions []Question `json:"b"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`^s[123]_`)
	var out []Question
	for _, q := range payload.Questions {
		if re.MatchString(q.ID) {
			out = append(out, q)
		}
	}
	return out, nil
}

func submitAnswers(client *http.Client, leid, token, referer string,
	cfg Config, encoded []string) (string, error) {

	type ansItem struct{ A string `json:"a"` }
	items := make([]ansItem, len(encoded))
	for i, a := range encoded {
		items[i] = ansItem{A: a}
	}
	payload := map[string]interface{}{
		"leid": leid, "tokenleid": token,
		"data": []interface{}{
			map[string]string{"id": "a", "a": cfg.Department, "b": cfg.EmployeeID, "c": cfg.Name},
			map[string]interface{}{"id": "b", "c": items},
		},
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST",
		"https://www.examcoo.com/editor/rpc/saveexam", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("User-Agent", ua)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", "https://www.examcoo.com")
	req.Header.Set("Referer", referer)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func scorePageURL(leid, token string) string {
	return fmt.Sprintf(
		"https://www.examcoo.com/index/viewascore/leid/%s/tokenleid/%s",
		leid, token)
}

// ─────────────────────────────────────────────────────────────
// 成绩解析
// ─────────────────────────────────────────────────────────────

type ScoreResult struct {
	Score       float64
	Total       float64
	PerQuestion map[int]bool
}

func fetchScoreDetails(client *http.Client, leid, token, referer string,
	questions []Question) (*ScoreResult, error) {

	url := fmt.Sprintf(
		"https://www.examcoo.com/index/viewascore/leid/%s/tokenleid/%s/embed/1",
		leid, token)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", referer)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	html := string(b)

	res := &ScoreResult{}

	// 解析实际得分
	for _, p := range []string{
		`(?:得分|score|分数)[^\d]*(\d+(?:\.\d+)?)`,
		`(\d+(?:\.\d+)?)\s*分`,
		`"score"\s*:\s*"?(\d+(?:\.\d+)?)`,
	} {
		if m := regexp.MustCompile(`(?i)` + p).FindStringSubmatch(html); m != nil {
			if v, e := strconv.ParseFloat(m[1], 64); e == nil {
				res.Score = v
				break
			}
		}
	}

	// 优先从成绩页读满分
	for _, p := range []string{
		`满分\s*[：:]\s*(\d+(?:\.\d+)?)`,
		`总分\s*[：:]\s*(\d+(?:\.\d+)?)`,
		`"total"\s*:\s*"?(\d+(?:\.\d+)?)`,
		`"full_score"\s*:\s*"?(\d+(?:\.\d+)?)`,
	} {
		if m := regexp.MustCompile(`(?i)` + p).FindStringSubmatch(html); m != nil {
			if v, e := strconv.ParseFloat(m[1], 64); e == nil && v > 0 {
				res.Total = v
				break
			}
		}
	}
	// 从题目 JSON 计算满分作为兜底
	if res.Total == 0 {
		t, _ := calcTotalScore(questions)
		res.Total = float64(t)
	}

	// 逐题解析
	perQ := tryParsePerQuestion(html, len(questions))
	if perQ != nil {
		res.PerQuestion = perQ
		return res, nil
	}
	res.PerQuestion = deduceByScore(res.Score, res.Total, questions)
	return res, nil
}

func tryParsePerQuestion(html string, n int) map[int]bool {
	if n == 0 {
		return nil
	}
	// 策略 A：ExamCoo 图片标记（i=对，h=错）
	reImg := regexp.MustCompile(`(?i)/v\d+_(\d+[hi])\.(?:bmp|png|gif|jpg)`)
	imgM := reImg.FindAllStringSubmatch(html, -1)
	if len(imgM) >= n {
		tail := imgM
		if len(imgM) > n {
			tail = imgM[len(imgM)-n:]
		}
		result := make(map[int]bool, n)
		for i, m := range tail {
			result[i+1] = strings.HasSuffix(strings.ToLower(m[1]), "i")
		}
		return result
	}
	// 策略 B：class right/wrong
	reR := regexp.MustCompile(`(?i)class=["'][^"']*\b(?:right|correct|answer-right)\b[^"']*["']`)
	reW := regexp.MustCompile(`(?i)class=["'][^"']*\b(?:wrong|incorrect|answer-wrong)\b[^"']*["']`)
	rights := reR.FindAllStringIndex(html, -1)
	wrongs := reW.FindAllStringIndex(html, -1)
	if len(rights)+len(wrongs) == n {
		type pos struct {
			idx int
			ok  bool
		}
		var all []pos
		for _, r := range rights {
			all = append(all, pos{r[0], true})
		}
		for _, w := range wrongs {
			all = append(all, pos{w[0], false})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].idx < all[j].idx })
		result := make(map[int]bool, n)
		for i, p := range all {
			result[i+1] = p.ok
		}
		return result
	}
	// 策略 C：JSON scores 数组
	reArr := regexp.MustCompile(`"(?:scores?|result)"\s*:\s*\[([^\]]+)\]`)
	if m := reArr.FindStringSubmatch(html); m != nil {
		parts := strings.Split(m[1], ",")
		if len(parts) == n {
			result := make(map[int]bool, n)
			any := false
			for i, p := range parts {
				if f, e := strconv.ParseFloat(strings.TrimSpace(p), 64); e == nil && f > 0 {
					result[i+1] = true
					any = true
				}
			}
			if any {
				return result
			}
		}
	}
	// 策略 D：data-result
	reD := regexp.MustCompile(`data-(?:result|correct)\s*=\s*["']([01])["']`)
	dm := reD.FindAllStringSubmatch(html, -1)
	if len(dm) == n {
		result := make(map[int]bool, n)
		for i, m := range dm {
			result[i+1] = m[1] == "1"
		}
		return result
	}
	// 策略 E：答对/答错文字
	reT := regexp.MustCompile(`答([对错])`)
	tm := reT.FindAllStringSubmatch(html, -1)
	if len(tm) == n {
		result := make(map[int]bool, n)
		for i, m := range tm {
			result[i+1] = m[1] == "对"
		}
		return result
	}
	return nil
}

func deduceByScore(score, pageTotal float64, questions []Question) map[int]bool {
	if score <= 0 || len(questions) == 0 {
		return nil
	}
	type qi struct {
		idx   int
		score int
	}
	var infos []qi
	for i, q := range questions {
		s := parseQuestionScore(q.Score)
		if s == 0 && pageTotal > 0 {
			s = int(pageTotal) / len(questions)
		}
		if s == 0 {
			s = 5
		}
		infos = append(infos, qi{i + 1, s})
	}
	total := 0
	for _, info := range infos {
		total += info.score
	}
	missed := float64(total) - score
	if missed == 0 {
		r := make(map[int]bool, len(infos))
		for _, info := range infos {
			r[info.idx] = true
		}
		return r
	}
	// 单题
	var wrongOne []int
	for _, info := range infos {
		if float64(info.score) == missed {
			wrongOne = append(wrongOne, info.idx)
		}
	}
	if len(wrongOne) == 1 {
		r := make(map[int]bool, len(infos))
		for _, info := range infos {
			r[info.idx] = info.idx != wrongOne[0]
		}
		return r
	}
	// 两题
	found := false
	var wp [2]int
	for i := 0; i < len(infos); i++ {
		for j := i + 1; j < len(infos); j++ {
			if float64(infos[i].score+infos[j].score) == missed {
				if !found {
					wp = [2]int{infos[i].idx, infos[j].idx}
					found = true
				} else {
					found = false
					goto done
				}
			}
		}
	}
done:
	if found {
		r := make(map[int]bool, len(infos))
		for _, info := range infos {
			r[info.idx] = info.idx != wp[0] && info.idx != wp[1]
		}
		return r
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// AI / API 工具
// ─────────────────────────────────────────────────────────────

// FetchModels 调用 /v1/models 拉取模型列表
func FetchModels(baseURL, apiKey, proxyURL string) ([]string, error) {
	client := newHTTPClient(15*time.Second, proxyURL)
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("API Key 无效 (401)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("服务器返回 %d: %s",
			resp.StatusCode, truncRune(string(body), 80))
	}
	// 解析标准 OpenAI 格式
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	var ids []string
	if err = json.Unmarshal(body, &result); err == nil {
		for _, m := range result.Data {
			if m.ID != "" {
				ids = append(ids, m.ID)
			}
		}
	}
	// 尝试直接字符串数组
	if len(ids) == 0 {
		var arr []string
		if json.Unmarshal(body, &arr) == nil {
			ids = arr
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// TestAPIKey 发送最小请求验证连通性，返回延迟
func TestAPIKey(baseURL, apiKey, model, proxyURL string) (time.Duration, error) {
	client := newHTTPClient(20*time.Second, proxyURL)
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	body, _ := json.Marshal(map[string]interface{}{
		"model":      model,
		"messages":   []msg{{Role: "user", Content: "hi"}},
		"max_tokens": 1,
	})
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return latency, fmt.Errorf("连接失败: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case 200, 201:
		return latency, nil
	case 401:
		return latency, fmt.Errorf("API Key 无效 (401)")
	case 404:
		return latency, fmt.Errorf("模型不存在或 Base URL 错误 (404)")
	default:
		return latency, fmt.Errorf("HTTP %d: %s",
			resp.StatusCode, truncRune(string(b), 80))
	}
}

const systemPrompt = `你是一位严谨、博学的考试专家，擅长各类知识性考题的分析与解答。
请逐题仔细审读，基于题目所属领域的专业知识、法规标准或常识进行推理，给出最准确的答案。

【输出格式规范】
每道题必须严格按照以下格式输出，每题独占一行：
  Q{序号}答案: {答案}

答案填写规则：
- 单选题 → 只填一个大写字母，例：A
- 多选题 → 填所有正确选项的大写字母（不加分隔符），例：ABD
- 判断题 → 只填 正确 或 错误

【示例输出】
Q1答案: B
Q2答案: ACD
Q3答案: 正确

【注意事项】
- 严格按格式输出，不得遗漏任何题目
- 答案行前后不加额外符号或说明
- 可在答案行之前附上简要分析（可选），但答案行格式不可更改`

func buildPrompt(questions []Question) string {
	lines := make([]string, 0, len(questions))
	for i, q := range questions {
		qt := qType(q)
		opts := parseOptions(q.Options)
		switch qt {
		case "s3":
			lines = append(lines, fmt.Sprintf("Q%d [判断题] %s", i+1, q.Text))
		default:
			label := "单选题"
			if qt == "s2" {
				label = "多选题"
			}
			op := make([]string, len(opts))
			for j, o := range opts {
				op[j] = fmt.Sprintf("%c.%s", 'A'+j, o.Text)
			}
			lines = append(lines, fmt.Sprintf("Q%d [%s] %s\n  %s",
				i+1, label, q.Text, strings.Join(op, "  ")))
		}
	}
	return strings.Join(lines, "\n\n")
}

func aiRound(cfg Config, questions []Question) (map[int]string, error) {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	body, _ := json.Marshal(map[string]interface{}{
		"model": cfg.Model,
		"messages": []msg{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: buildPrompt(questions)},
		},
		"temperature": 0.3,
	})
	req, _ := http.NewRequest("POST",
		strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := newHTTPClient(180*time.Second, cfg.ProxyURL)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API %d: %s", resp.StatusCode, truncRune(string(b), 120))
	}
	var cr struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, err
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("API 返回空 choices")
	}
	raw := cr.Choices[0].Message.Content
	result := map[int]string{}
	for i := 1; i <= len(questions); i++ {
		pat := fmt.Sprintf(`Q%d\s*答案\s*[:：\s]\s*(正确|错误|[A-Da-d]{1,8})`, i)
		if m := regexp.MustCompile(pat).FindStringSubmatch(raw); m != nil {
			ans := m[1]
			if ans != "正确" && ans != "错误" {
				ans = strings.ToUpper(ans)
			}
			result[i] = ans
		}
	}
	return result, nil
}

type Emit func(text, level string)

func aiVoting(cfg Config, questions []Question, emit Emit) (map[int]string, map[int]float64) {
	votes := make(map[int][]string, len(questions))
	for i := 1; i <= len(questions); i++ {
		votes[i] = nil
	}
	emit(fmt.Sprintf("[%s] 开始 %d 轮投票  模型: %s", ts(), cfg.VoteRounds, cfg.Model), "VOTE")
	for r := 0; r < cfg.VoteRounds; r++ {
		ans, err := aiRound(cfg, questions)
		if err != nil {
			emit(fmt.Sprintf("[%s]   ✗ 第 %d 轮失败: %v", ts(), r+1, err), "ERR")
		} else {
			for k, v := range ans {
				if v != "" {
					votes[k] = append(votes[k], v)
				}
			}
			valid := 0
			for _, v := range votes {
				if len(v) > 0 {
					valid++
				}
			}
			emit(fmt.Sprintf("[%s]   ✓ 第 %d/%d 轮完成  有效 %d/%d 题",
				ts(), r+1, cfg.VoteRounds, valid, len(questions)), "OK")
		}
		sleepDelay(cfg)
	}
	answers := make(map[int]string, len(questions))
	confidence := make(map[int]float64, len(questions))
	for i := 1; i <= len(questions); i++ {
		v := votes[i]
		qt := qType(questions[i-1])
		if len(v) == 0 {
			if qt == "s3" {
				answers[i] = "正确"
			} else {
				answers[i] = "A"
			}
			confidence[i] = 0
			continue
		}
		counter := map[string]int{}
		for _, a := range v {
			counter[a]++
		}
		best, bestN := "", 0
		for a, n := range counter {
			if n > bestN {
				best, bestN = a, n
			}
		}
		answers[i] = best
		confidence[i] = float64(bestN) / float64(len(v))
	}
	return answers, confidence
}

// ─────────────────────────────────────────────────────────────
// 核心任务
// ─────────────────────────────────────────────────────────────

func RunAutoExam(ctx context.Context, examURL string, cfg Config, emit Emit) {
	div := strings.Repeat("─", 64)
	client := makeClient(cfg.ProxyURL)
	bank := loadBank()

	emit(div, "INFO")
	emit(fmt.Sprintf("[%s] ▶ 自动答题开始  循环 %d 次  答题时间 %.0f 秒",
		ts(), cfg.LoopCount, cfg.SubmitDelay), "INFO")
	emit(fmt.Sprintf("[%s]   链接: %s", ts(), examURL), "INFO")
	emit(div, "INFO")

	for loop := 1; loop <= cfg.LoopCount; loop++ {
		if ctx.Err() != nil {
			emit(fmt.Sprintf("[%s] ⚠ 任务已取消", ts()), "WARN")
			return
		}
		if cfg.LoopCount > 1 {
			emit(fmt.Sprintf("[%s] ═══ 第 %d / %d 轮 ═══", ts(), loop, cfg.LoopCount), "INFO")
		}
		roundStart := time.Now()

		html, err := fetchPage(client, examURL)
		if err != nil {
			emit(fmt.Sprintf("[%s] ✗ 获取页面失败: %v", ts(), err), "ERR")
			return
		}
		sleepDelay(cfg)

		leid, token, err := extractIDs(html)
		if err != nil {
			emit(fmt.Sprintf("[%s] ✗ %v", ts(), err), "ERR")
			return
		}
		emit(fmt.Sprintf("[%s] ✓ leid=%s  token=%s...", ts(), leid, token[:8]), "OK")
		sleepDelay(cfg)

		emit(fmt.Sprintf("[%s] ↓ 拉取题目中...", ts()), "INFO")
		questions, err := fetchQuestions(client, leid, token, examURL)
		if err != nil {
			emit(fmt.Sprintf("[%s] ✗ 获取题目失败: %v", ts(), err), "ERR")
			return
		}
		if len(questions) == 0 {
			emit(fmt.Sprintf("[%s] ✗ 未解析到题目，请确认链接正确", ts()), "ERR")
			return
		}

		totalScore, hasReal := calcTotalScore(questions)
		if hasReal {
			emit(fmt.Sprintf("[%s] ✓ 共 %d 题，满分 %d 分（各题分值已读取）",
				ts(), len(questions), totalScore), "OK")
		} else {
			perQ := totalScore / len(questions)
			emit(fmt.Sprintf("[%s] ✓ 共 %d 题，满分估算 %d 分（每题约 %d 分，分值待成绩页确认）",
				ts(), len(questions), totalScore, perQ), "WARN")
		}
		sleepDelay(cfg)

		// 步骤1：题目未入库的先存入（题干+选项完整保留，答案留空）
		newAdded := 0
		for _, q := range questions {
			key := questionKey(q)
			if _, exists := bank[key]; !exists {
				opts := parseOptions(q.Options)
				optMap := make(map[string]string, len(opts))
				for j, o := range opts {
					if o.Text != "" {
						optMap[string(rune('A'+j))] = o.Text
					}
				}
				bank[key] = BankEntry{
					Answer:     "",
					Verified:   false,
					Text:       q.Text,
					QType:      qType(q),
					OptionsMap: optMap,
				}
				newAdded++
			}
		}
		if newAdded > 0 {
			_ = saveBank(bank)
			emit(fmt.Sprintf("[%s] ✓ 新增 %d 道题目结构已入库（答案待填写）", ts(), newAdded), "BANK")
		}

		// 步骤2：从题库取已校验答案
		bankAns := map[int]string{}
		for i, q := range questions {
			if e, ok := bank[questionKey(q)]; ok && e.Verified && e.Answer != "" {
				bankAns[i+1] = e.Answer
			}
		}
		if len(bankAns) > 0 {
			emit(fmt.Sprintf("[%s] ✓ 题库命中 %d/%d 题（已校验答案）",
				ts(), len(bankAns), len(questions)), "BANK")
		} else {
			emit(fmt.Sprintf("[%s]   题库无已校验答案，全部 AI 作答", ts()), "INFO")
		}

		aiAns := map[int]string{}
		conf := map[int]float64{}
		if len(bankAns) < len(questions) {
			aiAns, conf = aiVoting(cfg, questions, emit)
		} else {
			for i := 1; i <= len(questions); i++ {
				conf[i] = 1.0
			}
		}

		final := map[int]string{}
		for i := 1; i <= len(questions); i++ {
			if a, ok := bankAns[i]; ok {
				final[i] = a
				conf[i] = 1.0
			} else if a, ok := aiAns[i]; ok {
				final[i] = a
			} else {
				if qType(questions[i-1]) == "s3" {
					final[i] = "正确"
				} else {
					final[i] = "A"
				}
				conf[i] = 0
			}
		}

		encoded := make([]string, len(questions))
		for i, q := range questions {
			encoded[i] = encodeAnswer(final[i+1], qType(q))
		}

		// 答题预览
		emit(div, "INFO")
		emit(fmt.Sprintf("  %-4s %-4s %-26s  %-8s  %s", "#", "类型", "题目摘要", "答案", "来源/置信"), "INFO")
		emit(strings.Repeat("·", 64), "INFO")
		for i, q := range questions {
			src := "AI "
			if _, ok := bankAns[i+1]; ok {
				src = "库 "
			}
			pct := int(conf[i+1] * 100)
			emit(fmt.Sprintf("  Q%-3d [%s]  %-26s  %-8s  %s%3d%%",
				i+1, typeLabel(qType(q)), truncRune(q.Text, 26),
				final[i+1], src, pct), "INFO")
		}
		emit(div, "INFO")

		// 提交前延迟（确保总答题时间不低于设定值，0 表示不限制）
		if cfg.SubmitDelay > 0 {
			elapsed := time.Since(roundStart).Seconds()
			if elapsed < cfg.SubmitDelay {
				remain := cfg.SubmitDelay - elapsed
				emit(fmt.Sprintf("[%s] ⏳ 补足剩余时间 %.1f 秒...", ts(), remain), "INFO")
				time.Sleep(time.Duration(remain*1000) * time.Millisecond)
			} else {
				emit(fmt.Sprintf("[%s] ✓ 已答题 %.1f 秒，超过设定的 %.1f 秒，直接提交", ts(), elapsed, cfg.SubmitDelay), "INFO")
			}
		}

		emit(fmt.Sprintf("[%s] ↑ 提交答案...", ts()), "SEND")
		result, err := submitAnswers(client, leid, token, examURL, cfg, encoded)
		if err != nil {
			emit(fmt.Sprintf("[%s] ✗ 提交失败: %v", ts(), err), "ERR")
			return
		}
		emit(fmt.Sprintf("[%s] ✓ 提交完成  响应: %s", ts(), result), "OK")

		sleepDelay(cfg)
		sr, _ := fetchScoreDetails(client, leid, token, examURL, questions)

		sURL := scorePageURL(leid, token)
		emit(div, "INFO")
		emit(fmt.Sprintf("[%s] 📋 成绩查询链接（复制到浏览器打开）:", ts()), "INFO")
		emit("  "+sURL, "LINK")
		emit(div, "INFO")

		if sr == nil || sr.Score == 0 {
			emit(fmt.Sprintf("[%s] ⚠ 暂未读到成绩，请访问上方链接查看", ts()), "WARN")
			continue
		}

		emit(fmt.Sprintf("[%s] ★ 本次得分: %.0f / %.0f",
			ts(), sr.Score, sr.Total), "OK")

		verifiedCount, unverifiedCount := 0, 0
		deduceNote := ""
		if sr.PerQuestion != nil {
			deduceNote = " [逐题校验]"
		} else if sr.Score >= cfg.TargetScore {
			sr.PerQuestion = make(map[int]bool, len(questions))
			for i := range questions {
				sr.PerQuestion[i+1] = true
			}
			deduceNote = " [满分全标]"
		}

		// 步骤3：成绩回写题库
		for i, q := range questions {
			key := questionKey(q)
			oldEntry, hadOld := bank[key]

			var correct bool
			if sr.PerQuestion != nil {
				correct = sr.PerQuestion[i+1]
			} else {
				correct = sr.Score >= cfg.TargetScore
			}

			if correct {
				opts := parseOptions(q.Options)
				freshMap := make(map[string]string, len(opts))
				for j, o := range opts {
					if o.Text != "" {
						freshMap[string(rune('A'+j))] = o.Text
					}
				}
				finalOptMap := freshMap
				if len(finalOptMap) == 0 && hadOld && len(oldEntry.OptionsMap) > 0 {
					finalOptMap = oldEntry.OptionsMap
				}
				bank[key] = BankEntry{
					Answer:     final[i+1],
					Verified:   true,
					Text:       q.Text,
					QType:      qType(q),
					OptionsMap: finalOptMap,
				}
				verifiedCount++
			} else {
				fromBank := hadOld && oldEntry.Answer != ""
				if fromBank {
					if qType(q) == "s3" {
						bank[key] = BankEntry{
							Answer:     final[i+1],
							Verified:   false,
							Text:       q.Text,
							QType:      "s3",
							OptionsMap: nil,
						}
						emit(fmt.Sprintf("[%s]   判断题题库答案已修正为: %s", ts(), final[i+1]), "BANK")
					} else {
						delete(bank, key)
						emit(fmt.Sprintf("[%s]   删除题库中错误答案: %s", ts(), oldEntry.Answer), "BANK")
					}
				} else {
					if hadOld {
						opts := parseOptions(q.Options)
						if len(opts) > len(oldEntry.OptionsMap) {
							newMap := make(map[string]string, len(opts))
							for j, o := range opts {
								if o.Text != "" {
									newMap[string(rune('A'+j))] = o.Text
								}
							}
							oldEntry.OptionsMap = newMap
							bank[key] = oldEntry
						}
					}
				}
				unverifiedCount++
			}
		}
		_ = saveBank(bank)

		switch {
		case sr.Score >= cfg.TargetScore:
			emit(fmt.Sprintf("[%s] 🎉 满分！全部 %d 题写入题库%s",
				ts(), verifiedCount, deduceNote), "BANK")
		case sr.PerQuestion != nil:
			emit(fmt.Sprintf("[%s] ✓ 答对 %d 题已校验，%d 题待确认%s",
				ts(), verifiedCount, unverifiedCount, deduceNote), "BANK")
		default:
			emit(fmt.Sprintf("[%s] ⚠ 得分 %.0f/%.0f，无逐题数据，已存入题库（未校验）",
				ts(), sr.Score, sr.Total), "BANK")
		}

		// 轮次间等待
		if loop < cfg.LoopCount {
			waitSec := cfg.RequestDelay * 3
			emit(fmt.Sprintf("[%s] ⏳ 等待 %.1f 秒后开始下一轮...", ts(), waitSec), "INFO")
			time.Sleep(time.Duration(waitSec*1000) * time.Millisecond)
		}
	}
	emit(div, "INFO")
}

func RunBuildBank(ctx context.Context, examURL string, cfg Config, emit Emit) {
	div := strings.Repeat("─", 64)
	client := makeClient(cfg.ProxyURL)
	bank := loadBank()

	emit(div, "INFO")
	emit(fmt.Sprintf("[%s] ▶ 扫题入库（仅解析保存，不作答）  循环 %d 次", ts(), cfg.LoopCount), "INFO")
	emit(div, "INFO")

	for loop := 1; loop <= cfg.LoopCount; loop++ {
		if ctx.Err() != nil {
			emit(fmt.Sprintf("[%s] ⚠ 任务已取消", ts()), "WARN")
			return
		}
		if cfg.LoopCount > 1 {
			emit(fmt.Sprintf("[%s] ── 第 %d / %d 次 ──", ts(), loop, cfg.LoopCount), "INFO")
		}
		html, err := fetchPage(client, examURL)
		if err != nil {
			emit(fmt.Sprintf("[%s] ✗ %v", ts(), err), "ERR")
			continue
		}
		leid, token, err := extractIDs(html)
		if err != nil {
			emit(fmt.Sprintf("[%s] ✗ %v", ts(), err), "ERR")
			continue
		}
		emit(fmt.Sprintf("[%s] ✓ leid=%s", ts(), leid), "OK")
		sleepDelay(cfg)

		questions, err := fetchQuestions(client, leid, token, examURL)
		if err != nil {
			emit(fmt.Sprintf("[%s] ✗ %v", ts(), err), "ERR")
			continue
		}
		emit(fmt.Sprintf("[%s] ✓ 获取到 %d 题", ts(), len(questions)), "OK")

		// 扫题模式：仅解析保存，不调用 AI 作答，答案留空
		newCount := 0
		for _, q := range questions {
			key := questionKey(q)
			if _, ok := bank[key]; ok {
				continue // 已存在，跳过
			}
			opts := parseOptions(q.Options)
			optMap := make(map[string]string, len(opts))
			for j, o := range opts {
				optMap[string(rune('A'+j))] = o.Text
			}
			bank[key] = BankEntry{
				Answer:     "",   // 留空，待后续答题或手动填写
				Verified:   false,
				Text:       q.Text,
				QType:      qType(q),
				OptionsMap: optMap,
			}
			newCount++
		}
		if newCount == 0 {
			emit(fmt.Sprintf("[%s] ✓ 本轮无新题（共 %d 题已在库中）", ts(), len(questions)), "OK")
			sleepDelay(cfg)
			continue
		}
		_ = saveBank(bank)
		emit(fmt.Sprintf("[%s] ✓ 新增 %d 道题目已入库（答案留空，可在编辑页填写），题库共 %d 题",
			ts(), newCount, len(bank)), "BANK")

		if loop < cfg.LoopCount {
			w := cfg.RequestDelay * 3
			emit(fmt.Sprintf("[%s]   等待 %.0f 秒后继续扫题...", ts(), w), "INFO")
			time.Sleep(time.Duration(w*1000) * time.Millisecond)
		}
	}
	emit(div, "INFO")
	total, verified := BankStats()
	emit(fmt.Sprintf("[%s] ✓ 扫题完成！题库共 %d 题（其中满分校验 %d 题，未作答待填写 %d 题）",
		ts(), total, verified, total-verified), "OK")
	emit(div, "INFO")
}
