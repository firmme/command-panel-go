import './style.css';
import {GetCommands, GetArchivedCommands, AddCommand, UpdateCommand, DeleteCommand, DuplicateCommand, StartCommand, StopCommand, ArchiveCommand, RestoreCommand, GetLog, ClearLog, ReorderCommands, GetRunStates, InstallAutostart, UninstallAutostart, IsAutostartInstalled, HideWindow} from '../wailsjs/go/main/App';
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
const formPanel = document.getElementById('form-panel');
const logPanel = document.getElementById('log-panel');
const logContent = document.getElementById('log-content');
const logCmdName = document.getElementById('log-cmd-name');
const logStatus = document.getElementById('log-status');
const commandForm = document.getElementById('command-form');
const formTitle = document.getElementById('form-title');

// Initialize
async function init() {
    await refreshData();
    setupEventListeners();
    setupWailsEvents();
    setupPowerSaving();
    startPolling();
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
    // Add command button
    document.getElementById('btn-add').addEventListener('click', () => {
        editingId = null;
        formTitle.textContent = '新增命令';
        resetForm();
        formPanel.style.display = formPanel.style.display === 'none' ? 'block' : 'none';
    });

    // Close form
    document.getElementById('btn-close-form').addEventListener('click', () => {
        formPanel.style.display = 'none';
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
            }
        }
    });

    // Close log
    document.getElementById('btn-close-log').addEventListener('click', () => {
        logPanel.style.display = 'none';
        currentLogId = null;
    });
}

// Render command list (full re-render, only called when commands change)
function renderCommands() {
    if (!commands || commands.length === 0) {
        commandList.innerHTML = '<div class="empty-state"><p>暂无命令</p><p style="font-size:13px">点击上方"新增命令"添加</p></div>';
        return;
    }

    commandList.innerHTML = '';
    commands.forEach((cmd) => {
        const card = createCommandCard(cmd, false);
        commandList.appendChild(card);
    });
}

// Update only status indicators without replacing DOM
function updateCommandStates() {
    document.querySelectorAll('.command-card[data-id]').forEach(card => {
        const id = card.dataset.id;
        const state = runStates[id] || {};
        const isRunning = state.running;
        const exitCode = state.exitCode;
        const pid = state.pid;

        // Update status dot
        const dot = card.querySelector('.status-dot');
        if (dot) {
            dot.className = 'status-dot';
            if (isRunning) dot.classList.add('running');
            else if (pid > 0 && exitCode !== 0) dot.classList.add('error');
            else if (pid > 0 && exitCode === 0) dot.classList.add('success');
            else dot.classList.add('idle');
        }

        // Update duration detail in card-details
        const durationDetailEl = card.querySelector('.detail-item[title="已运行"], .detail-item[title="持续时间"]');
        if (durationDetailEl) {
            if (state.duration) {
                durationDetailEl.textContent = state.duration;
                durationDetailEl.title = isRunning ? '已运行' : '持续时间';
            } else {
                durationDetailEl.textContent = '';
            }
        } else if (state.duration) {
            // Insert duration detail after process info
            const processInfoEl = card.querySelector('.detail-item[title="进程"]');
            const idEl = card.querySelector('.detail-item[title="ID"]');
            const insertAfter = processInfoEl || idEl;
            if (insertAfter) {
                const newSpan = document.createElement('span');
                newSpan.className = 'detail-item';
                newSpan.title = isRunning ? '已运行' : '持续时间';
                newSpan.textContent = state.duration;
                insertAfter.after(newSpan);
            }
        }

        // Update start/stop button
        const actions = card.querySelector('.card-actions');
        if (actions) {
            const startBtn = actions.querySelector('[data-action="start"]');
            const stopBtn = actions.querySelector('[data-action="stop"]');
            if (isRunning && startBtn) {
                const btn = document.createElement('button');
                btn.className = 'btn btn-small btn-danger';
                btn.dataset.action = 'stop';
                btn.textContent = '停止';
                btn.addEventListener('click', async (e) => {
                    e.stopPropagation();
                    await handleAction('stop', id);
                });
                startBtn.replaceWith(btn);
            } else if (!isRunning && stopBtn) {
                const btn = document.createElement('button');
                btn.className = 'btn btn-small btn-success';
                btn.dataset.action = 'start';
                btn.textContent = '启动';
                btn.addEventListener('click', async (e) => {
                    e.stopPropagation();
                    await handleAction('start', id);
                });
                stopBtn.replaceWith(btn);
            }
        }
    });
}

// Format file size
function formatSize(bytes) {
    if (bytes === 0) return '0B';
    if (bytes < 1024) return bytes + 'B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + 'KB';
    return (bytes / (1024 * 1024)).toFixed(1) + 'MB';
}

// Format time as short string
function formatTime(t) {
    if (!t) return '';
    const d = new Date(t);
    const pad = n => String(n).padStart(2, '0');
    return `${pad(d.getMonth()+1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// Create a command card element
function createCommandCard(cmd, isArchive) {
    const card = document.createElement('div');
    card.className = 'command-card';
    card.dataset.id = cmd.id;
    card.draggable = false;

    const state = runStates[cmd.id] || {};
    const isRunning = state.running;
    const exitCode = state.exitCode;
    const pid = state.pid;

    let statusClass = 'idle';
    if (isRunning) statusClass = 'running';
    else if (pid > 0 && exitCode !== 0) statusClass = 'error';
    else if (pid > 0 && exitCode === 0) statusClass = 'success';

    let durationText = '';
    if (state.duration) {
        durationText = state.duration;
    } else if (pid > 0 && exitCode !== 0) {
        durationText = `exited(${exitCode})`;
    } else if (pid > 0 && exitCode === 0) {
        durationText = '✓';
    }

    // Process info
    let processInfo = '';
    if (isRunning && pid > 0) {
        const pname = state.cmd ? state.cmd.command.split(/\s+/)[0] : '';
        processInfo = `<span class="detail-item" title="进程">${escapeHtml(pname)}#${pid}</span>`;
    }

    // Duration detail
    let durationDetail = '';
    if (state.duration) {
        durationDetail = `<span class="detail-item" title="${isRunning ? '已运行' : '持续时间'}">${state.duration}</span>`;
    }

    // Log info
    const logName = state.logPath || cmd.id + '.log';
    const logSize = state.logSize || 0;
    const logInfo = `<span class="detail-item" title="日志文件">${escapeHtml(logName)} ${formatSize(logSize)}</span>`;

    // CWD
    const cwdText = cmd.cwd || '(默认)';

    // Auto-start
    const autoText = cmd.autoStart ? '是' : '否';

    // Update time
    const updateText = formatTime(cmd.updatedAt);

    // Short ID
    const shortId = cmd.id.length > 8 ? cmd.id.slice(-8) : cmd.id;

    const handleHTML = isArchive ? '' : '<span class="drag-handle" title="拖动排序">⋮⋮</span>';

    card.innerHTML = `
        <div class="card-body">
            <div class="card-content">
                <div class="card-header">
                    ${handleHTML}
                    <div class="status-dot ${statusClass}"></div>
                    <span class="card-name">${escapeHtml(cmd.name || inferName(cmd.command))}</span>
                </div>
                <div class="card-command">${highlightCommand(cmd.command)}</div>
                ${cmd.note ? `<div class="card-note">${escapeHtml(cmd.note)}</div>` : ''}
                <div class="card-details">
                    <span class="detail-item" title="ID">ID:${shortId}</span>
                    ${processInfo}
                    ${durationDetail}
                    <span class="detail-item" title="工作目录">目录:${escapeHtml(cwdText)}</span>
                    <span class="detail-item" title="自启">自启:${autoText}</span>
                    <span class="detail-item" title="日志文件">日志:${logInfo}</span>
                    <span class="detail-item" title="更新时间">更新:${updateText}</span>
                </div>
            </div>
            <div class="card-actions">
                ${isArchive ? `
                    <div class="btn-row">
                        <button class="btn btn-small btn-secondary" data-action="restore">恢复</button>
                        <button class="btn btn-small btn-secondary" data-action="edit">编辑</button>
                    </div>
                    <div class="btn-row btn-row-danger">
                        <button class="btn btn-xs btn-danger" data-action="delete">删除</button>
                    </div>
                ` : `
                    <div class="btn-row">
                        ${isRunning
                            ? `<button class="btn btn-small btn-danger" data-action="stop">停止</button>`
                            : `<button class="btn btn-small btn-success" data-action="start">启动</button>`
                        }
                        <button class="btn btn-small btn-secondary" data-action="log">日志</button>
                        <button class="btn btn-small btn-secondary" data-action="edit">编辑</button>
                        <button class="btn btn-small btn-secondary" data-action="duplicate">复制</button>
                    </div>
                    <div class="btn-row btn-row-danger">
                        <button class="btn btn-xs btn-outline" data-action="archive">归档</button>
                        <button class="btn btn-xs btn-danger" data-action="delete">删除</button>
                    </div>
                `}
            </div>
        </div>
    `;

    // Action handlers
    card.querySelectorAll('[data-action]').forEach(btn => {
        btn.addEventListener('click', async (e) => {
            e.stopPropagation();
            const action = btn.dataset.action;
            await handleAction(action, cmd.id);
        });
    });

    // Drag handle: enable drag only when clicking on the handle
    if (!isArchive) {
        const handle = card.querySelector('.drag-handle');
        if (handle) {
            handle.addEventListener('mousedown', () => {
                card.draggable = true;
            });
            document.addEventListener('mouseup', () => {
                card.draggable = false;
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
            showLog(id);
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
    await refreshLog();
}

// Refresh log content
async function refreshLog() {
    if (!currentLogId) return;
    try {
        const content = await GetLog(currentLogId);
        logContent.textContent = content || '(无日志)';
        logContent.scrollTop = logContent.scrollHeight;

        const state = runStates[currentLogId] || {};
        if (state.running) {
            logStatus.textContent = `PID: ${state.pid} | 运行中`;
            logStatus.style.color = 'var(--success)';
        } else if (state.pid > 0) {
            logStatus.textContent = `退出码: ${state.exitCode}`;
            logStatus.style.color = state.exitCode === 0 ? 'var(--success)' : 'var(--danger)';
        } else {
            logStatus.textContent = '';
        }
    } catch (e) {
        console.error('Failed to get log:', e);
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
    document.getElementById('cmd-note').value = cmd.note || '';
    document.getElementById('cmd-autostart').checked = cmd.autoStart || false;
    formPanel.style.display = 'block';
}

// Save command
async function saveCommand() {
    const id = document.getElementById('cmd-id').value;
    const cmd = {
        name: document.getElementById('cmd-name').value.trim(),
        command: document.getElementById('cmd-command').value.trim(),
        cwd: document.getElementById('cmd-cwd').value.trim(),
        note: document.getElementById('cmd-note').value.trim(),
        autoStart: document.getElementById('cmd-autostart').checked,
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
        formPanel.style.display = 'none';
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
            archiveList.innerHTML = '<div class="empty-state"><p>暂无归档命令</p></div>';
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

// Utility functions
function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
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
        // Separator && ||
        if (esc.slice(i, i + 5) === '&amp;&amp;') {
            parts.push('<span class="cmd-sep">&amp;&amp;</span>');
            i += 5;
            isFirst = true;
            continue;
        }
        if (esc.slice(i, i + 4) === '||') {
            parts.push('<span class="cmd-sep">||</span>');
            i += 4;
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
            if (esc.slice(j, j + 5) === '&amp;&amp;') break;
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

function formatSeconds(seconds) {
    if (seconds < 60) return `${seconds}s`;
    const m = Math.floor(seconds / 60);
    const s = seconds % 60;
    if (m < 60) return `${m}m${s}s`;
    const h = Math.floor(m / 60);
    return `${h}h${m % 60}m`;
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
