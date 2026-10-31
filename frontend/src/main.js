import './style.css';
import {GetCommands, GetArchivedCommands, DeleteCommand, DuplicateCommand, StartCommand, StopCommand, ArchiveCommand, RestoreCommand, ReorderCommands, GetRunStates, InstallAutostart, UninstallAutostart, IsAutostartInstalled, HideWindow, OpenURL, PickExecutable, CheckForUpdates, IgnoreUpdate, InstallUpdate} from '../bindings/command-panel/app.js';
import {Events} from '@wailsio/runtime';
import {escapeHtml, executableCommand} from './format.js';
import {createCommandForm} from './form.js';
import {createCardRenderer} from './cards.js';
import {createLogPanel} from './log-panel.js';
import {createRefreshQueue} from './refresh-queue.js';

// State
let commands = [];
let runStates = {};
let showArchive = false;
let lastCommandsJSON = '';
let lastStatesJSON = '';
let pollingTimer = null;
let idleTimer = null;
let isSuspended = false;

const POLL_INTERVAL_MS = 2000;
const IDLE_HIDE_MS = 10 * 60 * 1000;

// DOM elements
const archiveList = document.getElementById('archive-list');
const archivePanel = document.getElementById('archive-panel');
const logPanel = document.getElementById('log-panel');
const logContent = document.getElementById('log-content');
const commandForm = document.getElementById('command-form');
const formTitle = document.getElementById('form-title');
const formOverlay = document.getElementById('form-overlay');

// Inline SVG icon set (stroke style, 16x16 viewBox)
const ICONS = {
    play: '<path d="M5.5 3.2v9.6l7.5-4.8z" fill="currentColor" stroke="none"/>',
    stop: '<rect x="4" y="4" width="8" height="8" rx="1.5" fill="currentColor" stroke="none"/>',
    terminal: '<path d="M2.5 3.5h11v9h-11z"/><path d="M4.8 6.2l1.8 1.8-1.8 1.8M8.2 10h3"/>',
    pencil: '<path d="M2.5 13.5l.4-2.1L11 3.3l1.7 1.7-8.1 8.1z"/>',
    copy: '<rect x="5.5" y="5.5" width="8" height="8" rx="1.2"/><path d="M10.5 5.5V3.7a1.2 1.2 0 0 0-1.2-1.2H3.7a1.2 1.2 0 0 0-1.2 1.2v5.6a1.2 1.2 0 0 0 1.2 1.2h1.8"/>',
    trash: '<path d="M3 4.5h10M6.5 4.5v-1h3v1M4.5 4.5l.5 9h6l.5-9M6.5 7.5v4M9.5 7.5v4"/>',
    box: '<path d="M2.5 4h11M3.5 4v8a1 1 0 0 0 1 1h7a1 1 0 0 0 1-1V4M6.5 7.5h3"/>',
    restore: '<path d="M2.5 8a5.5 5.5 0 1 0 1.6-3.9M2.5 2.5v2.6h2.6"/>',
    grip: '<circle cx="6" cy="3.5" r="1" fill="currentColor" stroke="none"/><circle cx="10" cy="3.5" r="1" fill="currentColor" stroke="none"/><circle cx="6" cy="8" r="1" fill="currentColor" stroke="none"/><circle cx="10" cy="8" r="1" fill="currentColor" stroke="none"/><circle cx="6" cy="12.5" r="1" fill="currentColor" stroke="none"/><circle cx="10" cy="12.5" r="1" fill="currentColor" stroke="none"/>',
    folder: '<path d="M1.5 12.5v-8h4L7 6h7.5v6.5z"/>',
    zap: '<path d="M8.5 1.5L3 9h4l-1 5.5L12 7H8z"/>',
    volumeOn: '<path d="M2.5 6.5h2.5L8 3.5v9L5 9.5H2.5z"/><path d="M10.5 6a3.2 3.2 0 0 1 0 4M12.3 4.2a5.8 5.8 0 0 1 0 7.6"/>',
    volumeOff: '<path d="M2.5 6.5h2.5L8 3.5v9L5 9.5H2.5z"/><path d="M10.5 6.2l3.2 3.2M13.7 6.2l-3.2 3.2"/>',
    x: '<path d="M4 4l8 8M12 4l-8 8"/>',
    hamburger: '<path d="M2.5 4h11M2.5 8h11M2.5 12h11"/>',
    plus: '<path d="M8 3.5v9M3.5 8h9"/>',
};

function icon(name) {
    return `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[name]}</svg>`;
}

// ── 音效:进程异常退出时警报音 ─────────────────────
let audioCtx = null;

function soundEnabled() {
    return localStorage.getItem('soundEnabled') !== '0';
}

function ensureAudio() {
    if (!audioCtx) {
        try {
            audioCtx = new (window.AudioContext || window.webkitAudioContext)();
        } catch (e) {
            return null;
        }
    }
    if (audioCtx.state === 'suspended') {
        audioCtx.resume();
    }
    return audioCtx;
}

function tone(freq, start, dur, vol) {
    const ctx = ensureAudio();
    if (!ctx || ctx.state !== 'running') return;
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();
    const t = ctx.currentTime + start;
    osc.type = 'square';
    osc.frequency.value = freq;
    gain.gain.setValueAtTime(0.0001, t);
    gain.gain.exponentialRampToValueAtTime(vol, t + 0.012);
    gain.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    osc.connect(gain);
    gain.connect(ctx.destination);
    osc.start(t);
    osc.stop(t + dur + 0.02);
}

function playAlertSound() {
    if (!soundEnabled()) return;
    tone(660, 0, 0.14, 0.06);
    tone(440, 0.18, 0.22, 0.06);
}

function playConfirmSound() {
    if (!soundEnabled()) return;
    tone(880, 0, 0.09, 0.045);
}

// AudioContext needs a user gesture before it can play
document.addEventListener('click', () => {
    if (soundEnabled()) ensureAudio();
});

// Initialize
async function init() {
    await setupEngineOptions();
    await requestRefresh(true);
    setupEventListeners();
    setupWailsEvents();
    setupPowerSaving();
    startPolling();
    checkForUpdates(false);
}

// ── 执行引擎 ─────────────────────────────────────
function setupEngineOptions() {
    document.getElementById('cmd-engine').innerHTML = `
        <option value="">PowerShell(自动最新)</option>
        <option value="powershell5">Windows PowerShell 5.1</option>
        <option value="cmd">CMD</option>`;
}

async function refreshData(full) {
    const [newCommands, newStates] = await Promise.all([
        full ? GetCommands() : Promise.resolve(commands), GetRunStates(),
    ]);
    const commandData = newCommands || [];
    const stateData = newStates?.states || {};
    const commandsHash = JSON.stringify(commandData);
    const statesHash = JSON.stringify(stateData);
    const commandsChanged = commandsHash !== lastCommandsJSON;
    const statesChanged = statesHash !== lastStatesJSON;
    commands = commandData;
    runStates = stateData;
    lastCommandsJSON = commandsHash;
    lastStatesJSON = statesHash;
    if (commandsChanged) { renderCommands(); logs.updateButtons(); }
    else if (statesChanged) updateCommandStates();
    if (full && showArchive) await renderArchiveList();
    if (logs.currentId && !commands.some(cmd => cmd.id === logs.currentId)) logs.close();
    lastRefreshError = '';
    await logs.refresh();
}

function requestRefresh(full = false) { return refreshQueue.request(full); }

// Setup Wails event listeners
function setupWailsEvents() {
    Events.On('commands-updated', () => {
        if (isSuspended) return;
        requestRefresh(true);
    });
    // Pushed by backend when a process exits - update states without full re-render
    Events.On('run-state-changed', ({data: [id, exitCode, stopped]}) => {
        if (exitCode !== 0 && !stopped) playAlertSound();
        if (!isSuspended) requestRefresh();
    });
    Events.On('operation-error', ({data}) => showToast(data, true));
}

function setupPowerSaving() {
    const activityEvents = ['mousemove', 'mousedown', 'keydown', 'wheel', 'touchstart', 'dragstart'];
    activityEvents.forEach(eventName => {
        window.addEventListener(eventName, markActive, {passive: true});
    });

    document.addEventListener('visibilitychange', () => {
        if (document.hidden) {
            suspendFrontend();
        } else {
            resumeFrontend();
        }
    });

    window.addEventListener('focus', resumeFrontend);
    resetIdleTimer();
}

function markActive() {
    if (isSuspended) {
        resumeFrontend();
    }
    resetIdleTimer();
}

function resetIdleTimer() {
    if (idleTimer) {
        clearTimeout(idleTimer);
    }
    idleTimer = setTimeout(async () => {
        suspendFrontend();
        try {
            await HideWindow();
        } catch (e) {
            console.error('Failed to hide idle window:', e);
        }
    }, IDLE_HIDE_MS);
}

function suspendFrontend() {
    isSuspended = true;
    stopPolling();
}

async function resumeFrontend() {
    if (!isSuspended) {
        resetIdleTimer();
        return;
    }
    isSuspended = false;
    resetIdleTimer();
    startPolling();
    await requestRefresh(true);
}

// Setup DOM event listeners
function setupEventListeners() {
    // Sound toggle
    const soundBtn = document.getElementById('btn-sound');
    const syncSoundBtn = () => {
        soundBtn.classList.toggle('on', soundEnabled());
        soundBtn.innerHTML = icon(soundEnabled() ? 'volumeOn' : 'volumeOff');
        soundBtn.title = soundEnabled() ? '异常退出音效:已开启' : '异常退出音效:已关闭';
    };
    syncSoundBtn();
    soundBtn.addEventListener('click', () => {
        const enable = !soundEnabled();
        localStorage.setItem('soundEnabled', enable ? '1' : '0');
        syncSoundBtn();
        if (enable) {
            ensureAudio();
            playConfirmSound();
        }
        showToast(enable ? '异常退出音效已开启' : '异常退出音效已关闭');
    });

    // Auto-detect ${var} placeholders while typing the command
    document.getElementById('cmd-command').addEventListener('input', rebuildVarRows);

    // Pick an executable: fills command (quoted if needed) and working directory
    document.getElementById('btn-pick-exe').addEventListener('click', async () => {
        try {
            const pick = await PickExecutable();
            if (!pick || !pick.path) return;
            document.getElementById('cmd-command').value = executableCommand(pick.path, document.getElementById('cmd-engine').value);
            document.getElementById('cmd-cwd').value = pick.dir || '';
            rebuildVarRows();
        } catch (e) {
            console.error('Failed to pick executable:', e);
        }
    });

    // Add command button
    document.getElementById('btn-add').addEventListener('click', () => {
        formTitle.textContent = '新增命令';
        resetForm();
        showFormModal();
    });

    // Close form
    document.getElementById('btn-close-form').addEventListener('click', hideFormModal);

    // Click on the overlay (outside the modal) closes it
    formOverlay.addEventListener('mousedown', (e) => {
        if (e.target === formOverlay) hideFormModal();
    });

    // Escape closes the modal
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape' && formOverlay.style.display !== 'none') {
            hideFormModal();
        }
    });

    // Archive view toggle
    document.getElementById('btn-archive-view').addEventListener('click', async () => {
        showArchive = !showArchive;
        archivePanel.style.display = showArchive ? 'block' : 'none';
        if (showArchive) {
            await renderArchiveList();
        }
    });

    document.getElementById('btn-close-archive').addEventListener('click', () => {
        showArchive = false;
        archivePanel.style.display = 'none';
    });

    // Autostart button
    document.getElementById('btn-autostart').addEventListener('click', async () => {
        const installed = await IsAutostartInstalled();
        if (installed) {
            const result = await UninstallAutostart();
            if (result) {
                showToast(result, true);
            } else {
                showToast('已卸载开机自启');
            }
        } else {
            const result = await InstallAutostart();
            if (result) {
                showToast(result, true);
            } else {
                showToast('已安装开机自启');
            }
        }
    });

    // Form submit
    commandForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        await saveCommand();
    });

    // Log panel toggle
    logPanel.querySelector('.log-header').addEventListener('click', (e) => {
        if (e.target.closest('.log-actions')) return;
        logPanel.classList.toggle('collapsed');
        if (!logPanel.classList.contains('collapsed')) logs.refresh();
    });

    // Clear log
    document.getElementById('btn-clear-log').addEventListener('click', () => logs.clear());

    // Close log
    document.getElementById('btn-close-log').addEventListener('click', logs.close);

    // External links (in logs and notes) open in the system browser
    document.addEventListener('click', (e) => {
        const link = e.target.closest('.ext-link');
        if (link) {
            e.preventDefault();
            e.stopPropagation();
            OpenURL(link.dataset.url);
        }
    });

    setupLogContextMenu();

    // Reset draggability on all cards (single listener, avoids per-card leaks)
    document.addEventListener('mouseup', () => {
        document.querySelectorAll('.command-card').forEach(c => {
            c.draggable = false;
        });
    });

    // Close the popup menu (log right-click / card hamburger)
    document.addEventListener('click', closeActiveMenu);
    window.addEventListener('blur', closeActiveMenu);
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') closeActiveMenu();
    });
}

// Handle card actions
async function handleAction(action, id) {
    try {
        switch (action) {
            case 'start':
                const startResult = await StartCommand(id);
                if (startResult) {
                    showToast(startResult, true);
                } else {
                    await logs.show(id);
                }
                break;
            case 'stop':
                const stopResult = await StopCommand(id);
                if (stopResult) {
                    showToast(stopResult, true);
                }
                break;
            case 'log':
                if (logs.currentId === id) {
                    logs.close();
                } else {
                    await logs.show(id);
                }
                break;
            case 'edit':
                editCommand(id);
                break;
            case 'duplicate':
                const duplicated = await DuplicateCommand(id);
                if (duplicated) {
                    showToast('已复制命令');
                } else {
                    showToast('复制失败: 未找到命令', true);
                }
                break;
            case 'archive':
                await ArchiveCommand(id);
                break;
            case 'restore':
                await RestoreCommand(id);
                break;
            case 'delete':
                if (confirm('确定删除此命令？')) {
                    await DeleteCommand(id);
                    if (logs.currentId === id) {
                        logs.close();
                    }
                }
                break;
        }
        if (!['edit', 'log'].includes(action)) await requestRefresh(['duplicate', 'archive', 'restore', 'delete'].includes(action));
    } catch (e) { showToast('操作失败: ' + e, true); }
}

// Reorder commands
async function reorderCommands(srcId, targetId) {
    const ids = commands.map(c => c.id);
    const srcIdx = ids.indexOf(srcId);
    const targetIdx = ids.indexOf(targetId);
    if (srcIdx === -1 || targetIdx === -1) return;

    ids.splice(srcIdx, 1);
    ids.splice(targetIdx, 0, srcId);
    try { await ReorderCommands(ids); await requestRefresh(true); }
    catch (e) { showToast("排序失败: " + e, true); }
}

// Render archive list
async function renderArchiveList() {
    try {
        const archived = await GetArchivedCommands();
        if (!archived || archived.length === 0) {
            archiveList.innerHTML = `
                <div class="empty-state">
                    ${icon('box')}
                    <p>没有归档命令</p>
                    <p class="empty-hint">卡片上的归档按钮会把命令收进这里</p>
                </div>`;
            return;
        }
        archiveList.innerHTML = '';
        archived.forEach(cmd => {
            const card = createCommandCard(cmd, true);
            archiveList.appendChild(card);
        });
    } catch (e) {
        showToast('读取归档失败: ' + e, true);
    }
}

// Polling - only updates states, not full re-render
function startPolling() {
    if (pollingTimer) return;
    const poll = async () => {
        pollingTimer = null;
        if (isSuspended || document.hidden) return;
        await requestRefresh();
        if (!isSuspended && !document.hidden) pollingTimer = setTimeout(poll, POLL_INTERVAL_MS);
    };
    pollingTimer = setTimeout(poll, POLL_INTERVAL_MS);
}

function stopPolling() {
    if (pollingTimer) clearTimeout(pollingTimer);
    pollingTimer = null;
}

// ── 通用弹出菜单(右键菜单 / 卡片汉堡菜单共用) ─────────
let activeMenu = null;

function closeActiveMenu() {
    if (activeMenu) {
        activeMenu.remove();
        activeMenu = null;
    }
}

// items: {icon?, label, disabled?, action}
function openMenuAt(x, y, items, alignRight = false) {
    closeActiveMenu();
    const menu = document.createElement('div');
    menu.className = 'context-menu';
    items.forEach(item => {
        const el = document.createElement('div');
        el.className = 'context-menu-item'
            + (item.disabled ? ' disabled' : '')
            + (item.danger ? ' danger' : '');
        el.innerHTML = (item.icon ? icon(item.icon) : '') + `<span>${escapeHtml(item.label)}</span>`;
        if (!item.disabled) {
            el.addEventListener('click', () => {
                closeActiveMenu();
                item.action();
            });
        }
        menu.appendChild(el);
    });
    document.body.appendChild(menu);

    const rect = menu.getBoundingClientRect();
    let px = alignRight ? x - rect.width : x;
    let py = y;
    px = Math.max(6, Math.min(px, window.innerWidth - rect.width - 6));
    py = Math.max(6, Math.min(py, window.innerHeight - rect.height - 6));
    menu.style.left = px + 'px';
    menu.style.top = py + 'px';
    activeMenu = menu;
}

// Context menu for the log panel: copy selection / copy all
function setupLogContextMenu() {
    logContent.addEventListener('contextmenu', (e) => {
        e.preventDefault();
        const selection = window.getSelection().toString();
        openMenuAt(e.clientX, e.clientY, [
            {
                label: '复制选中文本',
                disabled: !selection,
                action: () => copyToClipboard(selection),
            },
            {
                label: '复制全部日志',
                action: () => copyToClipboard(logContent.textContent),
            },
        ]);
    });
}

// Card hamburger menu (edit / duplicate / archive / delete)
function openCardMenu(anchor, id, isArchive) {
    const items = isArchive ? [
        {icon: 'pencil', label: '编辑', action: () => handleAction('edit', id)},
        {icon: 'trash', label: '删除', danger: true, action: () => handleAction('delete', id)},
    ] : [
        {icon: 'pencil', label: '编辑', action: () => handleAction('edit', id)},
        {icon: 'copy', label: '复制命令', action: () => handleAction('duplicate', id)},
        {icon: 'box', label: '归档', action: () => handleAction('archive', id)},
        {icon: 'trash', label: '删除', danger: true, action: () => handleAction('delete', id)},
    ];
    const r = anchor.getBoundingClientRect();
    openMenuAt(r.right, r.bottom + 4, items, true);
}

async function copyToClipboard(text) {
    if (!text) return;
    try {
        await navigator.clipboard.writeText(text);
        showToast('已复制');
    } catch (e) {
        // Fallback for older WebView2 versions
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        ta.remove();
        showToast('已复制');
    }
}

// Utility functions
function showToast(message, isError = false) {
    const toast = document.createElement('div');
    toast.className = 'toast' + (isError ? ' error' : '');
    toast.textContent = message;
    document.body.appendChild(toast);
    setTimeout(() => toast.remove(), 3000);
}

let lastRefreshError = '';
const refreshQueue = createRefreshQueue(refreshData, e => {
    const message = String(e);
    if (message !== lastRefreshError) { lastRefreshError = message; showToast('刷新失败: ' + message, true); }
});
const logs = createLogPanel({getCommands: () => commands || [], getRunStates: () => runStates, showToast});
const {renderCommands, updateCommandStates, createCommandCard} = createCardRenderer({
    getCommands: () => commands, getRunStates: () => runStates, getCurrentLogId: () => logs.currentId,
    icon, handleAction, openCardMenu, reorderCommands,
});
const {showFormModal, hideFormModal, editCommand, rebuildVarRows, saveCommand, resetForm} = createCommandForm({
    getCommands: () => commands || [], icon, showToast, onSaved: () => requestRefresh(true),
});

// Start the app
init().catch(e => showToast('初始化失败: ' + e, true));

// Updates are checked at startup, but installation always requires confirmation.
let availableUpdate = null;
const updateOverlay = document.getElementById('update-overlay');
const updateButtons = ['update-ignore', 'update-later', 'update-install'].map(id => document.getElementById(id));
async function checkForUpdates(manual) {
    const button = document.getElementById('btn-check-update');
    if (button.disabled) return;
    button.disabled = true;
    try {
        const update = await CheckForUpdates(manual);
        if (!update?.version) { if (manual) showToast(`当前已是最新版本 (${update.currentVersion})`); return; }
        availableUpdate = update;
        document.getElementById('update-version').textContent = `${update.currentVersion} → ${update.version}`;
        document.getElementById('update-notes').textContent = update.notes || '此版本没有发布说明。';
        document.getElementById('update-status').textContent = update.ignored ? '此版本之前已被忽略。' : '';
        updateOverlay.style.display = 'flex';
        document.getElementById('update-install').focus();
    } catch (error) { if (manual) showToast(`检查更新失败：${error}`, true); }
    finally { button.disabled = false; }
}
document.getElementById('btn-check-update').addEventListener('click', () => checkForUpdates(true));
document.getElementById('update-later').addEventListener('click', () => { updateOverlay.style.display = 'none'; });
document.getElementById('update-ignore').addEventListener('click', async () => {
    try { await IgnoreUpdate(availableUpdate.version); updateOverlay.style.display = 'none'; }
    catch (error) { document.getElementById('update-status').textContent = `保存失败：${error}`; }
});
document.getElementById('update-install').addEventListener('click', async () => {
    updateButtons.forEach(button => { button.disabled = true; });
    document.getElementById('update-status').textContent = '正在下载和校验更新，请稍候…';
    try { await InstallUpdate(); }
    catch (error) { document.getElementById('update-status').textContent = `更新失败：${error}`; updateButtons.forEach(button => { button.disabled = false; }); }
});
