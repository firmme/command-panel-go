import './style.css';
import {GetCommands, GetArchivedCommands, AddCommand, UpdateCommand, DeleteCommand, DuplicateCommand, StartCommand, StopCommand, ArchiveCommand, RestoreCommand, GetLog, ClearLog, ReorderCommands, GetRunStates, InstallAutostart, UninstallAutostart, IsAutostartInstalled, HideWindow, OpenURL, PickExecutable, GetPlatform} from '../wailsjs/go/main/App';
import {EventsOn} from '../wailsjs/runtime/runtime';

// State
let commands = [];
let runStates = {};
let currentLogId = null;
let showArchive = false;
let editingId = null;
let dragSrcEl = null;
let lastCommandsHash = '';
let lastStatesHash = '';
let pollingTimer = null;
let idleTimer = null;
let isSuspended = false;

const POLL_INTERVAL_MS = 2000;
const IDLE_HIDE_MS = 10 * 60 * 1000;

// DOM elements
const commandList = document.getElementById('command-list');
const archiveList = document.getElementById('archive-list');
const archivePanel = document.getElementById('archive-panel');
const logPanel = document.getElementById('log-panel');
const logContent = document.getElementById('log-content');
const logCmdName = document.getElementById('log-cmd-name');
const logStatus = document.getElementById('log-status');
const commandForm = document.getElementById('command-form');
const formTitle = document.getElementById('form-title');
const formOverlay = document.getElementById('form-overlay');

function showFormModal() {
    formOverlay.style.display = 'flex';
}

function hideFormModal() {
    formOverlay.style.display = 'none';
}

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
    await refreshData();
    setupEventListeners();
    setupWailsEvents();
    setupPowerSaving();
    startPolling();
}

// ── 执行引擎 ─────────────────────────────────────
const ENGINE_OPTIONS = {
    windows: [
        {value: '', label: 'PowerShell(自动最新)'},
        {value: 'powershell5', label: 'Windows PowerShell 5.1'},
        {value: 'cmd', label: 'CMD'},
    ],
    unix: [
        {value: '', label: 'sh(默认)'},
        {value: 'bash', label: 'bash'},
        {value: 'zsh', label: 'zsh'},
    ],
};

let currentPlatform = 'windows';

async function setupEngineOptions() {
    try {
        currentPlatform = await GetPlatform() || 'windows';
    } catch (e) {
        currentPlatform = 'windows';
    }
    const options = currentPlatform === 'windows' ? ENGINE_OPTIONS.windows : ENGINE_OPTIONS.unix;
    const select = document.getElementById('cmd-engine');
    select.innerHTML = options.map(o => `<option value="${o.value}">${o.label}</option>`).join('');
}

// Hash for change detection
function hashData(data) {
    return JSON.stringify(data);
}

// Refresh data from backend - only re-render when data changes
async function refreshData() {
    try {
        const newCommands = await GetCommands();
        const newStates = await GetRunStates() || {};

        const commandsHash = hashData(newCommands);
        const statesHash = hashData(newStates);

        const commandsChanged = commandsHash !== lastCommandsHash;
        const statesChanged = statesHash !== lastStatesHash;

        commands = newCommands;
        runStates = newStates;

        if (commandsChanged) {
            lastCommandsHash = commandsHash;
            lastStatesHash = statesHash;
            renderCommands();
        } else if (statesChanged) {
            lastStatesHash = statesHash;
            updateCommandStates();
        }

        if (currentLogId) {
            await refreshLog();
        }
    } catch (e) {
        console.error('Failed to refresh data:', e);
    }
}

// Force full re-render (after add/edit/delete/reorder)
async function forceRefresh() {
    try {
        commands = await GetCommands();
        runStates = await GetRunStates() || {};
        lastCommandsHash = hashData(commands);
        lastStatesHash = hashData(runStates);
        renderCommands();
        if (currentLogId) {
            await refreshLog();
        }
    } catch (e) {
        console.error('Failed to refresh data:', e);
    }
}

// Setup Wails event listeners
function setupWailsEvents() {
    EventsOn('commands-updated', () => {
        if (isSuspended) return;
        forceRefresh();
    });
    // Pushed by backend when a process exits - update states without full re-render
    EventsOn('run-state-changed', async (id, exitCode, stopped) => {
        // audible alert even when the UI is suspended (window hidden in tray)
        if (exitCode !== 0 && !stopped) {
            playAlertSound();
        }
        if (isSuspended) return;
        try {
            const newStates = await GetRunStates() || {};
            runStates = newStates;
            lastStatesHash = hashData(runStates);
            updateCommandStates();
            if (currentLogId) {
                await refreshLog();
            }
        } catch (e) {
            console.error('Failed to refresh state:', e);
        }
    });
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
    await forceRefresh();
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
            const needsQuotes = /\s/.test(pick.path) && !pick.path.startsWith('"');
            document.getElementById('cmd-command').value = needsQuotes ? `"${pick.path}"` : pick.path;
            document.getElementById('cmd-cwd').value = pick.dir || '';
            rebuildVarRows();
        } catch (e) {
            console.error('Failed to pick executable:', e);
        }
    });

    // Add command button
    document.getElementById('btn-add').addEventListener('click', () => {
        editingId = null;
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
    });

    // Clear log
    document.getElementById('btn-clear-log').addEventListener('click', async () => {
        if (currentLogId) {
            const result = await ClearLog(currentLogId);
            if (result) {
                showToast(result, true);
            } else {
                logContent.textContent = '';
                lastLogContent = '';
                logContent.dataset.logId = currentLogId;
            }
        }
    });

    // Close log
    document.getElementById('btn-close-log').addEventListener('click', closeLog);

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

// Render command list (full re-render, only called when commands change)
function renderCommands() {
    if (!commands || commands.length === 0) {
        commandList.innerHTML = `
            <div class="empty-state">
                ${icon('plus')}
                <p>还没有命令</p>
                <p class="empty-hint">点击右上角「新增命令」添加第一条</p>
            </div>`;
        return;
    }

    commandList.innerHTML = '';
    commands.forEach((cmd) => {
        const card = createCommandCard(cmd, false);
        commandList.appendChild(card);
    });
}

// Compute status key shared by dot, card edge and runtime text
function statusKey(state) {
    if (state.running) return 'running';
    if (state.stopped) return 'stopped';
    if (state.pid > 0 && state.exitCode !== 0) return 'error';
    if (state.pid > 0 && state.exitCode === 0) return 'success';
    return 'idle';
}

function runtimeText(state) {
    if (state.running && state.pid > 0) {
        return `PID ${state.pid} ${state.duration || ''}`.trim();
    }
    if (state.stopped) {
        return state.duration ? `已停止 ${state.duration}` : '已停止';
    }
    if (state.pid > 0 && state.exitCode !== 0) {
        return state.duration ? `exited(${state.exitCode}) ${state.duration}` : `exited(${state.exitCode})`;
    }
    if (state.pid > 0 && state.exitCode === 0) {
        return state.duration ? `✓ ${state.duration}` : '✓';
    }
    return '';
}

// Update only status indicators without replacing DOM
function updateCommandStates() {
    document.querySelectorAll('.command-card[data-id]').forEach(card => {
        const id = card.dataset.id;
        const state = runStates[id] || {};

        card.dataset.status = statusKey(state);

        // Update status dot
        const dot = card.querySelector('.status-dot');
        if (dot) {
            dot.className = 'status-dot';
            dot.classList.add(statusKey(state));
        }

        // Update runtime detail (PID / duration / exit info)
        const runtimeEl = card.querySelector('.detail-runtime');
        if (runtimeEl) {
            runtimeEl.textContent = runtimeText(state);
            runtimeEl.classList.toggle('stopped', !!state.stopped);
        }

        // Update start/stop button
        const actions = card.querySelector('.card-actions');
        if (actions) {
            const startBtn = actions.querySelector('[data-action="start"]');
            const stopBtn = actions.querySelector('[data-action="stop"]');
            if (state.running && startBtn) {
                const btn = document.createElement('button');
                btn.className = 'btn btn-stop';
                btn.dataset.action = 'stop';
                btn.innerHTML = icon('stop') + '<span>停止</span>';
                btn.addEventListener('click', async (e) => {
                    e.stopPropagation();
                    await handleAction('stop', id);
                });
                startBtn.replaceWith(btn);
            } else if (!state.running && stopBtn) {
                const btn = document.createElement('button');
                btn.className = 'btn btn-start';
                btn.dataset.action = 'start';
                btn.innerHTML = icon('play') + '<span>启动</span>';
                btn.addEventListener('click', async (e) => {
                    e.stopPropagation();
                    await handleAction('start', id);
                });
                stopBtn.replaceWith(btn);
            }
        }
    });
}

// Create a command card element
function createCommandCard(cmd, isArchive) {
    const card = document.createElement('div');
    card.className = 'command-card';
    card.dataset.id = cmd.id;
    card.dataset.status = statusKey(runStates[cmd.id] || {});
    card.draggable = false;

    const state = runStates[cmd.id] || {};
    const isRunning = !!state.running;

    // Runtime detail (PID / duration / exit info)
    const runtimeHtml = `<span class="detail-runtime${state.stopped ? ' stopped' : ''}">${escapeHtml(runtimeText(state))}</span>`;

    // Sensitive variable chips (names only, never values)
    const varChips = (cmd.variables || [])
        .map(v => `<span class="var-chip" title="敏感变量">${escapeHtml('${' + v.name + '}')}</span>`)
        .join('');

    // Working directory (only when custom)
    const cwdInfo = cmd.cwd
        ? `<span class="detail-item" title="工作目录">${icon('folder')}<span>${escapeHtml(cmd.cwd)}</span></span>`
        : '';

    // Autostart flag (only when enabled)
    const autoInfo = cmd.autoStart
        ? `<span class="detail-item" title="开机后自动运行">${icon('zap')}<span>自启</span></span>`
        : '';

    // Engine (only when explicitly chosen)
    const ENGINE_NAMES = {cmd: 'cmd', powershell5: 'PS 5.1', bash: 'bash', zsh: 'zsh'};
    const engineInfo = ENGINE_NAMES[cmd.engine]
        ? `<span class="detail-item" title="执行引擎"><span>${ENGINE_NAMES[cmd.engine]}</span></span>`
        : '';

    const handleHTML = isArchive ? '' : `<span class="drag-handle" title="拖动排序">${icon('grip')}</span>`;

    card.innerHTML = `
        <div class="card-body">
            <div class="card-content">
                <div class="card-header">
                    ${handleHTML}
                    <div class="status-dot ${statusKey(state)}"></div>
                    <span class="card-name">${escapeHtml(cmd.name || inferName(cmd.command))}</span>
                </div>
                <div class="card-command">${highlightCommand(cmd.command)}</div>
                ${cmd.note ? `<div class="card-note">${linkify(cmd.note)}</div>` : ''}
                <div class="card-details">
                    ${runtimeHtml}
                    ${varChips}
                    ${engineInfo}
                    ${cwdInfo}
                    ${autoInfo}
                </div>
            </div>
            <div class="card-actions">
                ${isArchive ? `
                    <button class="btn btn-ghost btn-small" data-action="restore" title="恢复到命令列表">${icon('restore')}<span>恢复</span></button>
                    <button class="mini-btn" data-action="more" title="更多操作">${icon('hamburger')}</button>
                ` : `
                    ${isRunning
                        ? `<button class="btn btn-stop" data-action="stop">${icon('stop')}<span>停止</span></button>`
                        : `<button class="btn btn-start" data-action="start">${icon('play')}<span>启动</span></button>`
                    }
                    <button class="btn btn-ghost btn-log${currentLogId === cmd.id ? ' active' : ''}" data-action="log" title="查看日志">${icon('terminal')}<span>日志</span></button>
                    <button class="mini-btn" data-action="more" title="更多操作">${icon('hamburger')}</button>
                `}
            </div>
        </div>
    `;

    // Action handlers
    card.querySelectorAll('[data-action]').forEach(btn => {
        btn.addEventListener('click', async (e) => {
            e.stopPropagation();
            const action = btn.dataset.action;
            if (action === 'more') {
                openCardMenu(btn, cmd.id, isArchive);
                return;
            }
            await handleAction(action, cmd.id);
        });
    });

    // Drag handle: enable drag only when clicking on the handle.
    // A single document-level mouseup listener (in setupEventListeners) resets it.
    if (!isArchive) {
        const handle = card.querySelector('.drag-handle');
        if (handle) {
            handle.addEventListener('mousedown', () => {
                card.draggable = true;
            });
        }

        card.addEventListener('dragstart', (e) => {
            dragSrcEl = card;
            card.classList.add('dragging');
            e.dataTransfer.effectAllowed = 'move';
            e.dataTransfer.setData('text/plain', cmd.id);
        });

        card.addEventListener('dragend', () => {
            card.classList.remove('dragging');
            card.draggable = false;
            document.querySelectorAll('.command-card').forEach(c => c.classList.remove('drag-over'));
        });

        card.addEventListener('dragover', (e) => {
            e.preventDefault();
            e.dataTransfer.dropEffect = 'move';
            card.classList.add('drag-over');
        });

        card.addEventListener('dragleave', () => {
            card.classList.remove('drag-over');
        });

        card.addEventListener('drop', async (e) => {
            e.preventDefault();
            card.classList.remove('drag-over');
            const srcId = e.dataTransfer.getData('text/plain');
            if (srcId !== cmd.id) {
                await reorderCommands(srcId, cmd.id);
            }
        });
    }

    return card;
}

// Handle card actions
async function handleAction(action, id) {
    switch (action) {
        case 'start':
            const startResult = await StartCommand(id);
            if (startResult) {
                showToast(startResult, true);
            } else {
                showLog(id);
            }
            break;
        case 'stop':
            const stopResult = await StopCommand(id);
            if (stopResult) {
                showToast(stopResult, true);
            }
            break;
        case 'log':
            if (currentLogId === id) {
                closeLog();
            } else {
                showLog(id);
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
            if (showArchive) await renderArchiveList();
            break;
        case 'delete':
            if (confirm('确定删除此命令？')) {
                await DeleteCommand(id);
                if (currentLogId === id) {
                    logPanel.style.display = 'none';
                    currentLogId = null;
                }
            }
            break;
    }
    await forceRefresh();
}

// Show log panel
async function showLog(id) {
    currentLogId = id;
    const cmd = commands.find(c => c.id === id);
    logCmdName.textContent = cmd ? (cmd.name || inferName(cmd.command)) : id;
    logPanel.style.display = 'flex';
    logPanel.classList.remove('collapsed');
    updateLogButtons();
    // The log panel takes vertical space - keep the card visible
    const card = commandList.querySelector(`.command-card[data-id="${id}"]`);
    if (card) {
        card.scrollIntoView({block: 'nearest', behavior: 'smooth'});
    }
    await refreshLog();
}

// Close log panel
function closeLog() {
    logPanel.style.display = 'none';
    currentLogId = null;
    updateLogButtons();
}

// Highlight the log button of the card whose log is open
function updateLogButtons() {
    document.querySelectorAll('.command-card').forEach(card => {
        const btn = card.querySelector('[data-action="log"]');
        if (btn) {
            btn.classList.toggle('active', card.dataset.id === currentLogId);
        }
    });
}

// Refresh log content
let lastLogContent = null;
async function refreshLog() {
    if (!currentLogId) return;
    try {
        const content = await GetLog(currentLogId);
        // Skip DOM rewrite when content is unchanged so text selection survives
        if (content === lastLogContent && logContent.dataset.logId === currentLogId) {
            updateLogStatus();
            return;
        }
        lastLogContent = content;
        logContent.dataset.logId = currentLogId;
        // Only autoscroll when the user is already near the bottom
        const nearBottom = logContent.scrollHeight - logContent.scrollTop - logContent.clientHeight < 40;
        logContent.innerHTML = content ? linkify(content) : '(无日志)';
        if (nearBottom) {
            logContent.scrollTop = logContent.scrollHeight;
        }
        updateLogStatus();
    } catch (e) {
        console.error('Failed to refresh log:', e);
    }
}

function updateLogStatus() {
        const state = runStates[currentLogId] || {};
        if (state.running) {
            logStatus.textContent = `PID: ${state.pid} | 运行中`;
            logStatus.style.color = 'var(--ok)';
        } else if (state.stopped) {
            logStatus.textContent = '已手动停止';
            logStatus.style.color = 'var(--text-2)';
        } else if (state.pid > 0) {
            logStatus.textContent = `退出码: ${state.exitCode}`;
            logStatus.style.color = state.exitCode === 0 ? 'var(--ok)' : 'var(--err)';
        } else {
            logStatus.textContent = '';
        }
}

// Edit command
function editCommand(id) {
    const cmd = commands.find(c => c.id === id);
    if (!cmd) {
        GetArchivedCommands().then(archived => {
            const aCmd = archived.find(c => c.id === id);
            if (aCmd) fillForm(aCmd);
        });
        return;
    }
    fillForm(cmd);
}

function fillForm(cmd) {
    editingId = cmd.id;
    formTitle.textContent = '编辑命令';
    document.getElementById('cmd-id').value = cmd.id;
    document.getElementById('cmd-name').value = cmd.name || '';
    document.getElementById('cmd-command').value = cmd.command || '';
    document.getElementById('cmd-cwd').value = cmd.cwd || '';
    document.getElementById('cmd-engine').value = cmd.engine || '';
    document.getElementById('cmd-note').value = cmd.note || '';
    document.getElementById('cmd-autostart').checked = cmd.autoStart || false;
    varValues = {};
    removedVars = new Set();
    (cmd.variables || []).forEach(v => {
        varValues[v.name] = v.value;
    });
    rebuildVarRows();
    showFormModal();
}

// ── 敏感变量:命令中的 ${名称} 占位符 ─────────────
let varValues = {};
let removedVars = new Set();

const VAR_RE = /\$\{([^}${\s]+)\}/g;

// Scan the command input for placeholders and (re)build the value rows.
// Deleted rows stay deleted until the form is re-opened.
function rebuildVarRows() {
    const cmdText = document.getElementById('cmd-command').value;
    const names = [...new Set([...cmdText.matchAll(VAR_RE)].map(m => m[1]))]
        .filter(n => !removedVars.has(n));

    const group = document.getElementById('var-group');
    const container = document.getElementById('var-container');
    group.style.display = names.length ? '' : 'none';
    container.innerHTML = '';

    names.forEach(name => {
        const row = document.createElement('div');
        row.className = 'var-row';
        row.dataset.name = name;

        const label = document.createElement('span');
        label.className = 'var-name';
        label.textContent = '${' + name + '}';

        const input = document.createElement('input');
        input.type = 'password';
        input.placeholder = '输入值,保存于本机';
        input.autocomplete = 'off';
        input.value = varValues[name] || '';
        input.addEventListener('input', () => {
            varValues[name] = input.value;
        });

        const remove = document.createElement('button');
        remove.type = 'button';
        remove.className = 'var-remove';
        remove.title = '删除此变量,占位符按普通文本保留';
        remove.innerHTML = icon('x');
        remove.addEventListener('click', () => {
            removedVars.add(name);
            delete varValues[name];
            row.remove();
            if (!container.children.length) group.style.display = 'none';
        });

        row.append(label, input, remove);
        container.appendChild(row);
    });
}

function collectVariables() {
    const vars = [];
    document.querySelectorAll('#var-container .var-row').forEach(row => {
        vars.push({name: row.dataset.name, value: varValues[row.dataset.name] || ''});
    });
    return vars;
}

// Save command
async function saveCommand() {
    const id = document.getElementById('cmd-id').value;
    const cmd = {
        name: document.getElementById('cmd-name').value.trim(),
        command: document.getElementById('cmd-command').value.trim(),
        cwd: document.getElementById('cmd-cwd').value.trim(),
        engine: document.getElementById('cmd-engine').value,
        note: document.getElementById('cmd-note').value.trim(),
        autoStart: document.getElementById('cmd-autostart').checked,
        variables: collectVariables(),
    };

    if (!cmd.command) {
        showToast('命令不能为空', true);
        return;
    }

    try {
        if (id) {
            cmd.id = id;
            await UpdateCommand(cmd);
            showToast('命令已更新');
        } else {
            await AddCommand(cmd);
            showToast('命令已添加');
        }
        hideFormModal();
        resetForm();
        await forceRefresh();
    } catch (e) {
        showToast('保存失败: ' + e, true);
    }
}

// Reset form
function resetForm() {
    commandForm.reset();
    document.getElementById('cmd-id').value = '';
    varValues = {};
    removedVars = new Set();
    document.getElementById('var-group').style.display = 'none';
    document.getElementById('var-container').innerHTML = '';
}

// Reorder commands
async function reorderCommands(srcId, targetId) {
    const ids = commands.map(c => c.id);
    const srcIdx = ids.indexOf(srcId);
    const targetIdx = ids.indexOf(targetId);
    if (srcIdx === -1 || targetIdx === -1) return;

    ids.splice(srcIdx, 1);
    ids.splice(targetIdx, 0, srcId);
    await ReorderCommands(ids);
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
        console.error('Failed to get archived commands:', e);
    }
}

// Polling - only updates states, not full re-render
function startPolling() {
    if (pollingTimer) return;
    pollingTimer = setInterval(async () => {
        if (isSuspended || document.hidden) return;
        try {
            const newStates = await GetRunStates() || {};
            const statesHash = hashData(newStates);
            if (statesHash !== lastStatesHash) {
                runStates = newStates;
                lastStatesHash = statesHash;
                updateCommandStates();
            }
            if (currentLogId) {
                await refreshLog();
            }
        } catch (e) {
            // Ignore polling errors
        }
    }, POLL_INTERVAL_MS);
}

function stopPolling() {
    if (!pollingTimer) return;
    clearInterval(pollingTimer);
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
function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

const URL_RE = /(https?:\/\/[^\s<>"')\]]+)/g;

// Escapes HTML then turns http(s) URLs into clickable links
function linkify(text) {
    return escapeHtml(text).replace(URL_RE, (m) => `<a class="ext-link" data-url="${m}">${m}</a>`);
}

function highlightCommand(cmd) {
    if (!cmd) return '';
    const esc = escapeHtml(cmd);
    const parts = [];
    let i = 0;
    let isFirst = true;
    while (i < esc.length) {
        // Quoted string
        if (esc[i] === '"' || esc[i] === "'") {
            const q = esc[i];
            let j = i + 1;
            while (j < esc.length && esc[j] !== q) j++;
            if (j < esc.length) j++;
            parts.push(`<span class="cmd-string">${esc.slice(i, j)}</span>`);
            i = j;
            isFirst = false;
            continue;
        }
        // Separator && || (escaped as &amp;&amp; / ||)
        if (esc.slice(i, i + 10) === '&amp;&amp;') {
            parts.push('<span class="cmd-sep">&amp;&amp;</span>');
            i += 10;
            isFirst = true;
            continue;
        }
        if (esc.slice(i, i + 2) === '||') {
            parts.push('<span class="cmd-sep">||</span>');
            i += 2;
            isFirst = true;
            continue;
        }
        // Pipe
        if (esc[i] === '|') {
            parts.push('<span class="cmd-pipe">|</span>');
            i++;
            isFirst = true;
            continue;
        }
        // Redirect
        if (esc.slice(i, i + 4) === '&gt;') {
            parts.push('<span class="cmd-pipe">&gt;</span>');
            i += 4;
            continue;
        }
        if (esc.slice(i, i + 4) === '&lt;') {
            parts.push('<span class="cmd-pipe">&lt;</span>');
            i += 4;
            continue;
        }
        // Semicolon
        if (esc[i] === ';') {
            parts.push('<span class="cmd-sep">;</span>');
            i++;
            isFirst = true;
            continue;
        }
        // Whitespace
        if (esc[i] === ' ' || esc[i] === '\t') {
            parts.push(esc[i]);
            i++;
            continue;
        }
        // Word
        let j = i;
        while (j < esc.length && esc[j] !== ' ' && esc[j] !== '\t' && esc[j] !== '|' && esc[j] !== ';' && esc[j] !== '"' && esc[j] !== "'") {
            if (esc.slice(j, j + 4) === '&gt;' || esc.slice(j, j + 4) === '&lt;') break;
            if (esc.slice(j, j + 10) === '&amp;&amp;') break;
            j++;
        }
        const word = esc.slice(i, j);
        let cls = '';
        if (isFirst) {
            cls = 'cmd-name';
            isFirst = false;
        } else if (word.startsWith('-')) {
            cls = 'cmd-flag';
        } else if (word.startsWith('$') || (word.startsWith('%') && word.endsWith('%'))) {
            cls = 'cmd-env';
        }
        parts.push(cls ? `<span class="${cls}">${word}</span>` : word);
        i = j;
    }
    return parts.join('');
}

function inferName(command) {
    if (!command) return '';
    const parts = command.split(/\s+/);
    return parts[0] || '';
}

function showToast(message, isError = false) {
    const toast = document.createElement('div');
    toast.className = 'toast' + (isError ? ' error' : '');
    toast.textContent = message;
    document.body.appendChild(toast);
    setTimeout(() => toast.remove(), 3000);
}

// Start the app
init();
