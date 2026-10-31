import {ReadLog, ClearLog} from '../bindings/command-panel/app.js';
import {inferName, linkify} from './format.js';

const MAX_DISPLAY_CHARS = 256 * 1024;

export function createLogPanel({getCommands, getRunStates, showToast, readLog = ReadLog, clearLog = ClearLog}) {
    const panel = document.getElementById('log-panel');
    const content = document.getElementById('log-content');
    const status = document.getElementById('log-status');
    let currentId = null;
    let generation = 0;
    let offset = 0;
    let version = '';
    let buffer = '';
    let inFlight = null;

    function reset() {
        generation++;
        offset = 0;
        version = '';
        buffer = '';
        content.textContent = '';
    }

    function updateButtons() {
        document.querySelectorAll('.command-card').forEach(card => {
            card.querySelector('[data-action="log"]')?.classList.toggle('active', card.dataset.id === currentId);
        });
    }

    function updateStatus() {
        const state = getRunStates()[currentId] || {};
        status.textContent = state.running ? `PID: ${state.pid} | 运行中`
            : state.stopped ? '已手动停止' : state.pid > 0 ? `退出码: ${state.exitCode}` : '';
        status.style.color = state.running || (state.pid > 0 && state.exitCode === 0)
            ? 'var(--ok)' : state.stopped ? 'var(--text-2)' : 'var(--err)';
    }

    const visible = () => currentId && !document.hidden && !panel.classList.contains('collapsed');

    async function refresh() {
        if (!visible()) return;
        if (inFlight) return inFlight;
        inFlight = (async () => {
            while (visible()) {
                const id = currentId;
                const requestedGeneration = generation;
                let chunk;
                try { chunk = await readLog(id, offset, version); }
                catch (e) {
                    if (requestedGeneration !== generation) continue;
                    showToast('读取日志失败: ' + e, true);
                    return;
                }
                if (requestedGeneration !== generation) continue;
                if (!visible()) return;
                const nearBottom = content.scrollHeight - content.scrollTop - content.clientHeight < 40;
                offset = chunk.offset;
                version = chunk.version;
                if (chunk.reset) buffer = '';
                const combined = buffer + chunk.text;
                const trimmed = combined.length > MAX_DISPLAY_CHARS;
                buffer = combined.slice(-MAX_DISPLAY_CHARS);
                if (chunk.reset || trimmed) content.innerHTML = linkify(buffer);
                else if (chunk.text) content.insertAdjacentHTML('beforeend', linkify(chunk.text));
                if (nearBottom) content.scrollTop = content.scrollHeight;
                updateStatus();
                return;
            }
        })();
        try { await inFlight; } finally { inFlight = null; }
    }

    async function show(id) {
        if (id !== currentId) { currentId = id; reset(); }
        const cmd = getCommands().find(c => c.id === id);
        document.getElementById('log-cmd-name').textContent = cmd ? cmd.name || inferName(cmd.command) : id;
        panel.style.display = 'flex';
        panel.classList.remove('collapsed');
        updateButtons();
        updateStatus();
        document.querySelectorAll('#command-list .command-card').forEach(card => {
            if (card.dataset.id === id) card.scrollIntoView({block: 'nearest', behavior: 'smooth'});
        });
        await refresh();
    }

    function close() {
        currentId = null;
        reset();
        panel.style.display = 'none';
        updateButtons();
    }

    async function clear() {
        const id = currentId;
        if (!id) return;
        try {
            const error = await clearLog(id);
            if (error) { showToast(error, true); return; }
            if (currentId === id) { reset(); await refresh(); }
        } catch (e) { showToast('清空日志失败: ' + e, true); }
    }

    return {get currentId() { return currentId; }, show, close, refresh, clear, updateButtons};
}
