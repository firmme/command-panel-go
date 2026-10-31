import {escapeHtml, linkify, highlightCommand, inferName} from './format.js';

export function createCardRenderer({getCommands, getRunStates, getCurrentLogId, icon, handleAction, openCardMenu, reorderCommands}) {
    const commandList = document.getElementById('command-list');
    function renderCommands() {
        const commands = getCommands();
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
            const state = getRunStates()[id] || {};

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
        card.dataset.status = statusKey(getRunStates()[cmd.id] || {});
        card.draggable = false;

        const state = getRunStates()[cmd.id] || {};
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
                        <button class="btn btn-ghost btn-log${getCurrentLogId() === cmd.id ? ' active' : ''}" data-action="log" title="查看日志">${icon('terminal')}<span>日志</span></button>
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

    return {renderCommands, updateCommandStates, createCommandCard};
}
