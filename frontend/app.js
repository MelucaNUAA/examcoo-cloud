// ── 状态 ──
let allModels = [];
let selectedModelIdx = -1;
let bankEditRows = [];
let bankEditFiltered = [];
let selectedEditRow = -1;
let bankViewRows = [];
let ws = null;
let currentTaskId = null;

// ── API 工具函数 ──
async function api(method, path, body) {
    const opts = {
        method,
        headers: { 'Content-Type': 'application/json' },
    };
    if (body !== undefined) {
        opts.body = JSON.stringify(body);
    }
    const resp = await fetch('/api' + path, opts);
    return resp.json();
}

// ── WebSocket ──
let wsReconnectDelay = 1000;
const WS_MAX_RECONNECT_DELAY = 30000;
let wsShouldReconnect = true;

function connectWS(taskId) {
    // Prevent multiple simultaneous connections
    if (ws) {
        wsShouldReconnect = false; // Disable reconnect for old connection
        ws.close();
        ws = null;
    }
    wsShouldReconnect = true;

    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = taskId
        ? proto + '//' + location.host + '/ws?task_id=' + taskId
        : proto + '//' + location.host + '/ws?task_id=_global';
    ws = new WebSocket(url);
    ws.onopen = () => {
        wsReconnectDelay = 1000; // Reset delay on successful connection
    };
    ws.onmessage = (e) => {
        try {
            const msg = JSON.parse(e.data);
            if (msg.type === 'log') {
                appendLog(msg.data.text, msg.data.level);
            } else if (msg.type === 'task-state') {
                document.getElementById('btn-start').disabled = msg.data.running;
                document.getElementById('btn-stop').disabled = !msg.data.running;
                document.getElementById('status-text').textContent = msg.data.running ? '任务执行中...' : '就绪';
                if (!msg.data.running) {
                    currentTaskId = null;
                }
            } else if (msg.type === 'bank-stats') {
                updateBankStatsDisplay(msg.data.total, msg.data.verified);
            }
        } catch (err) {}
    };
    ws.onclose = () => {
        if (!wsShouldReconnect) return; // Skip reconnect if disabled
        // Exponential backoff for reconnection
        const delay = wsReconnectDelay;
        wsReconnectDelay = Math.min(wsReconnectDelay * 2, WS_MAX_RECONNECT_DELAY);
        setTimeout(() => {
            if (wsShouldReconnect) {
                if (currentTaskId) connectWS(currentTaskId);
                else connectWS();
            }
        }, delay);
    };
    ws.onerror = () => {};
}

// ── 初始化 ──
window.addEventListener('DOMContentLoaded', () => {
    loadConfig();
    refreshBankStats();
    connectWS();

    // 移动端默认收起 AI 配置区域
    if (window.innerWidth <= 768) {
        const section = document.getElementById('ai-config-section');
        if (section) {
            section.classList.add('collapsed');
        }
    }
});

// ── 折叠功能 ──
function toggleAISettings() {
    const section = document.getElementById('ai-config-section');
    if (section) {
        section.classList.toggle('collapsed');
    }
}

// ── 配置 ──
async function loadConfig() {
    const resp = await api('GET', '/config');
    const cfg = resp.data || resp;
    document.getElementById('user-name').value = cfg.name || '';
    document.getElementById('user-eid').value = cfg.employee_id || '';
    document.getElementById('user-dept').value = cfg.department || '';
    document.getElementById('base-url').value = cfg.base_url || '';
    document.getElementById('proxy-url').value = cfg.proxy_url || '';
    document.getElementById('api-key').value = cfg.api_key || '';
    document.getElementById('model-name').value = cfg.model || '';
    document.getElementById('vote-rounds').value = cfg.vote_rounds || 3;
    document.getElementById('loop-count').value = cfg.loop_count || 1;
    document.getElementById('request-delay').value = cfg.request_delay || 1;
    document.getElementById('submit-delay').value = cfg.submit_delay || 3;
}

function collectConfig() {
    return {
        name: document.getElementById('user-name').value.trim(),
        employee_id: document.getElementById('user-eid').value.trim(),
        department: document.getElementById('user-dept').value.trim(),
        base_url: document.getElementById('base-url').value.trim().replace(/\/+$/, ''),
        proxy_url: document.getElementById('proxy-url').value.trim(),
        api_key: document.getElementById('api-key').value.trim(),
        model: document.getElementById('model-name').value.trim(),
        vote_rounds: parseInt(document.getElementById('vote-rounds').value) || 3,
        loop_count: parseInt(document.getElementById('loop-count').value) || 1,
        request_delay: parseFloat(document.getElementById('request-delay').value) || 1,
        submit_delay: parseFloat(document.getElementById('submit-delay').value) || 3,
        target_score: 100,
    };
}

async function saveConfig() {
    const cfg = collectConfig();
    const resp = await api('PUT', '/config', cfg);
    const el = document.getElementById('status-text');
    if (resp.error) {
        el.textContent = '保存失败: ' + resp.error;
    } else {
        el.textContent = '配置已保存';
    }
    setTimeout(() => { el.textContent = '就绪'; }, 3000);
}

// ── API 测试 ──
async function testAPI() {
    const cfg = collectConfig();
    if (!cfg.base_url) { setStatus('请先填写 Base URL'); return; }
    if (!cfg.model) { setStatus('请先填写模型名称'); return; }
    appendLog('[' + ts() + '] 测试 API 连通性...', 'INFO');
    const resp = await api('POST', '/test-api', {
        base_url: cfg.base_url,
        api_key: cfg.api_key,
        model: cfg.model,
        proxy_url: cfg.proxy_url,
    });
    const el = document.getElementById('status-text');
    if (resp.error) {
        appendLog('[' + ts() + '] 测试失败: ' + resp.error, 'ERR');
        el.textContent = '测试失败: ' + resp.error;
    } else {
        appendLog('[' + ts() + '] 连通成功，延迟 ' + resp.data.latency + 'ms', 'OK');
        el.textContent = '连通成功 (' + resp.data.latency + 'ms)';
    }
    setTimeout(() => { el.textContent = '就绪'; }, 5000);
}

// ── 模型选择 ──
async function openModelPicker() {
    selectedModelIdx = -1;
    document.getElementById('model-search').value = '';
    document.getElementById('model-count').textContent = '正在加载...';
    renderModelList([]);
    showModal('modal-model');
    await fetchModels();
}

async function fetchModels() {
    const cfg = collectConfig();
    if (!cfg.base_url) { setStatus('请先填写 Base URL'); return; }
    document.getElementById('model-count').textContent = '拉取中，请稍候...';
    const resp = await api('POST', '/fetch-models', {
        base_url: cfg.base_url,
        api_key: cfg.api_key,
        proxy_url: cfg.proxy_url,
    });
    if (resp.error) {
        document.getElementById('model-count').textContent = '拉取失败: ' + resp.error;
        return;
    }
    allModels = resp.data || [];
    document.getElementById('model-count').textContent = '共 ' + allModels.length + ' 个模型';
    renderModelList(allModels);
}

function filterModels() {
    const kw = document.getElementById('model-search').value.toLowerCase().trim();
    const filtered = kw ? allModels.filter(m => m.toLowerCase().includes(kw)) : allModels;
    document.getElementById('model-count').textContent =
        kw ? '筛选 ' + filtered.length + ' / ' + allModels.length + ' 个' : '共 ' + allModels.length + ' 个模型';
    renderModelList(filtered);
}

function renderModelList(models) {
    const container = document.getElementById('model-list');
    container.innerHTML = '';
    models.forEach((m, i) => {
        const div = document.createElement('div');
        div.className = 'model-item ' + (i === selectedModelIdx ? 'selected' : '');
        div.textContent = m;
        div.onclick = () => {
            selectedModelIdx = i;
            container.querySelectorAll('.model-item').forEach(el => el.classList.remove('selected'));
            div.classList.add('selected');
        };
        container.appendChild(div);
    });
}

function useSelectedModel() {
    if (selectedModelIdx < 0 || selectedModelIdx >= allModels.length) {
        setStatus('请先选择一个模型');
        return;
    }
    document.getElementById('model-name').value = allModels[selectedModelIdx];
    closeModal('modal-model');
}

// ── 任务执行 ──
function onModeChange(mode) {
    if (mode === 'answer') {
        document.getElementById('loop-count').value = 1;
    }
}

async function startTask() {
    const url = document.getElementById('exam-url').value.trim();
    const cfg = collectConfig();
    const mode = document.querySelector('input[name="mode"]:checked').value;

    if (mode === 'answer' && cfg.loop_count > 1) {
        showConfirm('当前答题轮次为 ' + cfg.loop_count + '，将自动提交 ' + cfg.loop_count + ' 次。确定继续吗？', async () => {
            await doStartTask(url, cfg, mode);
        });
        return;
    }
    await doStartTask(url, cfg, mode);
}

async function doStartTask(url, cfg, mode) {
    // Generate a temporary task ID for WebSocket connection
    const tempTaskId = 'task_' + Date.now();
    connectWS(tempTaskId);

    const resp = await api('POST', '/task/start', { exam_url: url, config: cfg, mode: mode });
    if (resp.error) {
        setStatus(resp.error);
        return;
    }
    currentTaskId = resp.data.task_id;
    // Reconnect with the real task ID
    connectWS(currentTaskId);
}

function stopTask() {
    if (currentTaskId) {
        api('POST', '/task/stop', { task_id: currentTaskId });
    }
}

// ── 题库统计 ──
function updateBankStatsDisplay(total, verified) {
    const unverified = total - verified;
    document.getElementById('bank-stats').textContent = '题库: ' + total + ' 题 | 已校验: ' + verified + ' | 未校验: ' + unverified;
}

async function refreshBankStats() {
    const resp = await api('GET', '/bank/stats');
    const stats = resp.data || resp;
    updateBankStatsDisplay(stats.total, stats.verified);
}

// ── 查看题库 ──
async function openBankViewer() {
    const rowsResp = await api('GET', '/bank/rows');
    bankViewRows = rowsResp.data || rowsResp || [];
    const statsResp = await api('GET', '/bank/stats');
    const stats = statsResp.data || statsResp;
    document.getElementById('view-stats-total').textContent = stats.total;
    document.getElementById('view-stats-verified').textContent = stats.verified;
    document.getElementById('view-stats-unverified').textContent = stats.total - stats.verified;
    document.getElementById('view-filter-type').value = '';
    document.getElementById('view-filter-verify').value = '';
    document.getElementById('view-filter-search').value = '';
    applyViewerFilter();
    showModal('modal-bank-view');
}

function applyViewerFilter() {
    const typeF = document.getElementById('view-filter-type').value;
    const verifyF = document.getElementById('view-filter-verify').value;
    const kw = document.getElementById('view-filter-search').value.toLowerCase().trim();

    const filtered = bankViewRows.filter(r => {
        if (typeF && r.QType !== typeF) return false;
        if (verifyF === 'yes' && !r.Verified) return false;
        if (verifyF === 'no' && r.Verified) return false;
        if (kw && !r.Text.toLowerCase().includes(kw)) return false;
        return true;
    });

    document.getElementById('view-filter-count').textContent = filtered.length + ' / ' + bankViewRows.length + ' 题';

    const tbody = document.getElementById('bank-view-tbody');
    tbody.innerHTML = filtered.map((r, i) =>
        '<tr>' +
        '<td>' + (i + 1) + '</td>' +
        '<td>' + esc(r.Answer) + '</td>' +
        '<td>' + typeLabel(r.QType) + '</td>' +
        '<td>' + (r.Verified ? '<span style="color:#10b981;font-weight:bold;">满分</span>' : '—') + '</td>' +
        '<td class="col-stretch" title="' + esc(r.Text) + '">' + esc(r.Text) + '</td>' +
        '</tr>'
    ).join('');
}

// ── 编辑题库 ──
async function openBankEditor() {
    const rowsResp = await api('GET', '/bank/rows');
    bankEditRows = rowsResp.data || rowsResp || [];
    const statsResp = await api('GET', '/bank/stats');
    const stats = statsResp.data || statsResp;
    document.getElementById('edit-stats-total').textContent = stats.total;
    document.getElementById('edit-stats-verified').textContent = stats.verified;
    document.getElementById('edit-stats-unverified').textContent = stats.total - stats.verified;
    selectedEditRow = -1;
    document.getElementById('filter-type').value = '';
    document.getElementById('filter-verify').value = '';
    document.getElementById('filter-search').value = '';
    document.getElementById('edit-panel').style.display = 'none';
    applyEditorFilter();
    showModal('modal-bank-edit');
}

function applyEditorFilter() {
    const typeF = document.getElementById('filter-type').value;
    const verifyF = document.getElementById('filter-verify').value;
    const kw = document.getElementById('filter-search').value.toLowerCase().trim();

    bankEditFiltered = bankEditRows.filter(r => {
        if (typeF && r.QType !== typeF) return false;
        if (verifyF === 'yes' && !r.Verified) return false;
        if (verifyF === 'no' && r.Verified) return false;
        if (kw && !r.Text.toLowerCase().includes(kw)) return false;
        return true;
    });

    document.getElementById('filter-count').textContent = bankEditFiltered.length + ' / ' + bankEditRows.length + ' 题';

    const tbody = document.getElementById('bank-edit-tbody');
    tbody.innerHTML = '';

    bankEditFiltered.forEach((r, i) => {
        const tr = document.createElement('tr');
        tr.dataset.idx = i;
        if (i === selectedEditRow) tr.classList.add('selected');
        tr.innerHTML =
            '<td>' + (i + 1) + '</td>' +
            '<td style="font-weight:600; color:var(--primary);">' + esc(r.Answer) + '</td>' +
            '<td>' + typeLabel(r.QType) + '</td>' +
            '<td style="color:#10b981; font-weight:bold; font-size:14px;">' + (r.Verified ? '✓' : '') + '</td>' +
            '<td class="col-stretch" title="' + esc(r.Text) + '">' + esc(r.Text) + '</td>';
        tr.onclick = () => selectEditRow(i);
        tbody.appendChild(tr);
    });
}

function selectEditRow(idx) {
    selectedEditRow = idx;
    const tbody = document.getElementById('bank-edit-tbody');
    tbody.querySelectorAll('tr').forEach((tr, i) => {
        tr.classList.toggle('selected', i === idx);
    });

    const r = bankEditFiltered[idx];
    if (!r) return;

    const panel = document.getElementById('edit-panel');
    panel.style.display = 'block';

    const label = typeLabel(r.QType);
    const text = r.Text.length > 120 ? r.Text.substring(0, 120) + '...' : r.Text;
    document.getElementById('edit-question').textContent = '[' + label + ']  ' + esc(text);

    const optDiv = document.getElementById('edit-options');
    optDiv.innerHTML = '';
    if (r.QType !== 's3' && r.OptionsMap) {
        const keys = Object.keys(r.OptionsMap).sort();
        const inputType = r.QType === 's1' ? 'radio' : 'checkbox';
        const inputName = r.QType === 's1' ? 'opt-radio' : '';

        keys.forEach(k => {
            const div = document.createElement('div');
            div.className = 'opt-item';
            const isChecked = (r.Answer && r.Answer.toUpperCase().includes(k)) ? 'checked' : '';

            div.innerHTML =
                '<label style="display:flex; align-items:center; gap:6px; cursor:pointer;">' +
                '<input type="' + inputType + '" ' + (inputName ? 'name="' + inputName + '"' : '') + ' value="' + k + '" ' + isChecked + '>' +
                '<span><strong>' + k + '.</strong> ' + esc(r.OptionsMap[k]) + '</span>' +
                '</label>';
            div.querySelector('input').addEventListener('change', syncAnswerFromOpts);
            optDiv.appendChild(div);
        });
    }

    document.getElementById('edit-answer').value = r.Answer || '';
    document.getElementById('edit-verified').checked = r.Verified;
    document.getElementById('edit-status').textContent = '';
    document.getElementById('batch-status').textContent = '正在编辑：第 ' + (idx + 1) + ' 题';
}

function syncAnswerFromOpts() {
    const r = bankEditFiltered[selectedEditRow];
    if (!r) return;
    const optDiv = document.getElementById('edit-options');
    const inputs = optDiv.querySelectorAll('input');
    let ans = '';
    inputs.forEach(inp => {
        if (inp.checked) ans += inp.value;
    });
    document.getElementById('edit-answer').value = ans;
}

async function saveCurrentRow() {
    if (selectedEditRow < 0 || selectedEditRow >= bankEditFiltered.length) return;
    const r = bankEditFiltered[selectedEditRow];
    const answer = document.getElementById('edit-answer').value.trim();
    const verified = document.getElementById('edit-verified').checked;
    if (!answer) { setStatus('答案不能为空'); return; }

    const resp = await api('PUT', '/bank/entry', { key: r.Key, answer: answer, verified: verified });
    if (resp.error) {
        setStatus(resp.error);
        return;
    }

    r.Answer = answer;
    r.Verified = verified;
    const orig = bankEditRows.find(x => x.Key === r.Key);
    if (orig) { orig.Answer = answer; orig.Verified = verified; }

    updateEditStats();
    applyEditorFilter();
    document.getElementById('edit-status').textContent = '已保存  答案: ' + answer + '  状态: ' + (verified ? '已校验' : '未校验');

    setTimeout(() => {
        const statusEl = document.getElementById('edit-status');
        if (statusEl.textContent.includes('已保存')) statusEl.textContent = '';
    }, 3000);
}

function updateEditStats() {
    const total = bankEditRows.length;
    const verified = bankEditRows.filter(r => r.Verified).length;
    document.getElementById('edit-stats-total').textContent = total;
    document.getElementById('edit-stats-verified').textContent = verified;
    document.getElementById('edit-stats-unverified').textContent = total - verified;
}

// ── 确认弹窗 ──
let _confirmCallback = null;

function showConfirm(msg, onConfirm) {
    document.getElementById('confirm-msg').textContent = msg;
    _confirmCallback = onConfirm;
    showModal('modal-confirm');
}

function closeConfirm(ok) {
    closeModal('modal-confirm');
    if (ok && _confirmCallback) {
        const fn = _confirmCallback;
        _confirmCallback = null;
        fn();
    }
    _confirmCallback = null;
}

async function batchVerifyAll() {
    showConfirm('将全部题目标记为已校验？', async () => {
        const resp = await api('POST', '/bank/batch-verify');
        if (resp.error) { setStatus(resp.error); return; }
        bankEditRows.forEach(r => r.Verified = true);
        updateEditStats();
        applyEditorFilter();
        document.getElementById('batch-status').textContent = resp.data.message;
    });
}

async function batchUnverifyAll() {
    showConfirm('清除全部题目的校验状态？', async () => {
        const resp = await api('POST', '/bank/batch-unverify');
        if (resp.error) { setStatus(resp.error); return; }
        bankEditRows.forEach(r => r.Verified = false);
        updateEditStats();
        applyEditorFilter();
        document.getElementById('batch-status').textContent = resp.data.message;
    });
}

async function deleteSelectedRow() {
    if (selectedEditRow < 0 || selectedEditRow >= bankEditFiltered.length) {
        setStatus('请先选择一道题目');
        return;
    }
    const r = bankEditFiltered[selectedEditRow];
    showConfirm('确定删除「' + r.Text.substring(0, 20) + '...」？此操作不可恢复。', async () => {
        const resp = await api('DELETE', '/bank/entry/' + encodeURIComponent(r.Key));
        if (resp.error) { setStatus(resp.error); return; }
        bankEditRows = bankEditRows.filter(x => x.Key !== r.Key);
        selectedEditRow = -1;
        document.getElementById('edit-panel').style.display = 'none';
        updateEditStats();
        applyEditorFilter();
        document.getElementById('batch-status').textContent = resp.data.message;
    });
}

// ── 日志 ──
function appendLog(text, level) {
    const view = document.getElementById('log-view');
    const div = document.createElement('div');
    div.className = 'log-entry log-' + (level || 'INFO');
    div.textContent = text;
    view.appendChild(div);
    view.scrollTop = view.scrollHeight;
}

function clearLog() {
    document.getElementById('log-view').innerHTML = '';
}

// ── 工具 ──
function setStatus(msg, duration) {
    const el = document.getElementById('status-text');
    el.textContent = msg;
    setTimeout(() => { if (el.textContent === msg) el.textContent = '就绪'; }, duration || 3000);
}
function ts() {
    const d = new Date();
    return pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
}
function pad(n) { return n < 10 ? '0' + n : '' + n; }

function typeLabel(qt) {
    return { s1: '单选', s2: '多选', s3: '判断' }[qt] || '未知';
}

function esc(s) {
    if (!s) return '';
    let cleanText = s.replace(/&nbsp;/g, ' ');
    const d = document.createElement('div');
    d.textContent = cleanText;
    return d.innerHTML;
}

function showModal(id) { document.getElementById(id).style.display = 'flex'; }
function closeModal(id) { document.getElementById(id).style.display = 'none'; }

// ── 使用说明 ──
function openHelp() {
    showModal('modal-help');
}

function openLink(url) {
    window.open(url, '_blank');
}

// ── 二维码扫描 ──
let qrScanner = null;

function openQRScanner() {
    showModal('modal-qr');
    document.getElementById('qr-status').textContent = '';
    startQRCamera();
}

async function startQRCamera() {
    const container = document.getElementById('qr-reader');
    container.innerHTML = '';

    if (typeof Html5Qrcode === 'undefined') {
        document.getElementById('qr-status').textContent = '扫码库加载失败，请检查网络';
        return;
    }

    qrScanner = new Html5Qrcode('qr-reader');
    try {
        await qrScanner.start(
            { facingMode: 'environment' },
            {
                fps: 10,
                qrbox: { width: 250, height: 250 },
                aspectRatio: 1.0,
            },
            (decodedText) => {
                document.getElementById('exam-url').value = decodedText;
                closeQRScanner();
                setStatus('已从二维码填入链接');
            },
            () => {} // ignore errors during scanning
        );
    } catch (err) {
        document.getElementById('qr-status').textContent = '无法访问摄像头: ' + err.message;
    }
}

async function closeQRScanner() {
    if (qrScanner) {
        try {
            await qrScanner.stop();
        } catch (e) {}
        qrScanner = null;
    }
    closeModal('modal-qr');
}

function qrFromFile(input) {
    if (!input.files || !input.files[0]) return;
    if (typeof Html5Qrcode === 'undefined') {
        document.getElementById('qr-status').textContent = '扫码库加载失败';
        return;
    }
    const reader = new FileReader();
    reader.onload = async (e) => {
        const tempScanner = new Html5Qrcode('qr-reader');
        try {
            const result = await tempScanner.scanFile(input.files[0], true);
            document.getElementById('exam-url').value = result;
            closeQRScanner();
            setStatus('已从图片二维码填入链接');
        } catch (err) {
            document.getElementById('qr-status').textContent = '未识别到二维码: ' + err.message;
        }
    };
    reader.readAsDataURL(input.files[0]);
}
