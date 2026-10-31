import test from 'node:test';
import assert from 'node:assert/strict';
import {createRefreshQueue} from '../src/refresh-queue.js';
import {executableCommand} from '../src/format.js';
import {createLogPanel} from '../src/log-panel.js';

const deferred = () => {
    let resolve;
    const promise = new Promise(r => { resolve = r; });
    return {promise, resolve};
};

test('refresh requests coalesce and never overlap', async () => {
    const first = deferred();
    const started = deferred();
    const calls = [];
    let active = 0;
    let peak = 0;
    const queue = createRefreshQueue(async full => {
        active++;
        peak = Math.max(peak, active);
        calls.push(full);
        if (calls.length === 1) { started.resolve(); await first.promise; }
        active--;
    }, e => { throw e; }, 0);
    const request = queue.request(true);
    queue.request(true);
    queue.request(false);
    await started.promise;
    queue.request(false);
    queue.request(true);
    first.resolve();
    await request;
    assert.deepEqual(calls, [true, true]);
    assert.equal(peak, 1);
});

test('refresh queue recovers after a failed backend call', async () => {
    let calls = 0;
    const errors = [];
    const queue = createRefreshQueue(async () => { if (++calls === 1) throw new Error('read failed'); }, e => errors.push(e.message), 0);
    await queue.request(true);
    await queue.request(true);
    assert.equal(calls, 2);
    assert.deepEqual(errors, ['read failed']);
});

test('executable commands quote paths for the selected shell', () => {
    assert.equal(executableCommand("C:\\Program Files\\it's $app.exe", ''), "& 'C:\\Program Files\\it''s $app.exe'");
    assert.equal(executableCommand('C:\\Program Files\\app.exe', 'cmd'), '"C:\\Program Files\\app.exe"');
});

function logHarness(readLog) {
    const elements = new Map();
    function element() {
        const classes = new Set();
        let html = '';
        return {
            style: {}, scrollHeight: 0, scrollTop: 0, clientHeight: 0,
            classList: {contains: c => classes.has(c), remove: c => classes.delete(c), add: c => classes.add(c)},
            set textContent(value) { html = value; }, get textContent() { return html; },
            set innerHTML(value) { html = value; }, get innerHTML() { return html; },
            insertAdjacentHTML(_position, value) { html += value; },
        };
    }
    globalThis.document = {
        hidden: false,
        getElementById(id) { if (!elements.has(id)) elements.set(id, element()); return elements.get(id); },
        querySelectorAll() { return []; },
        createElement: element,
    };
    globalThis.window = {go: {main: {App: {ReadLog: readLog, ClearLog: async () => ''}}}};
    const errors = [];
    const panel = createLogPanel({getCommands: () => [], getRunStates: () => ({}), showToast: message => errors.push(message)});
    return {panel, elements, errors};
}

test('late log response cannot overwrite the newly selected command', async () => {
    const a = deferred();
    const calls = [];
    const {panel, elements} = logHarness(async id => {
        calls.push(id);
        return id === 'a' ? a.promise : {text: 'B log', offset: 5, version: 'b', reset: true};
    });
    const showingA = panel.show('a');
    const showingB = panel.show('b');
    a.resolve({text: 'A log', offset: 5, version: 'a', reset: true});
    await Promise.all([showingA, showingB]);
    assert.equal(panel.currentId, 'b');
    assert.equal(elements.get('log-content').textContent, 'B log');
    assert.deepEqual(calls, ['a', 'b']);
});

test('collapsed logs pause requests and clearing invalidates an in-flight response', async () => {
    const pending = deferred();
    const calls = [];
    const {panel, elements} = logHarness(async (id, offset, version) => {
        calls.push([id, offset, version]);
        return calls.length === 1 ? pending.promise : {text: '', offset: 0, version: 'new', reset: true};
    });
    const showing = panel.show('a');
    const clearing = panel.clear();
    await Promise.resolve();
    pending.resolve({text: 'stale log', offset: 9, version: 'old', reset: true});
    await Promise.all([showing, clearing]);
    assert.equal(elements.get('log-content').textContent, '');
    assert.equal(calls.length, 2);
    elements.get('log-panel').classList.add('collapsed');
    await panel.refresh();
    assert.equal(calls.length, 2);
});
