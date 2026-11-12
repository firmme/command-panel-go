export function escapeHtml(text) {
    return String(text).replace(/[&<>"']/g, ch => ({
        '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[ch]));
}

const URL_RE = /(https?:\/\/[^\s<>"')\]]+)/g;

// Escapes HTML then turns http(s) URLs into clickable links
export function linkify(text) {
    return escapeHtml(text).replace(URL_RE, (m) => `<a class="ext-link" data-url="${m}">${m}</a>`);
}

// Absolute Windows path: drive root, UNC share, or forward-slash variant.
const PATH_RE = /(?:[A-Za-z]:[\\/]|\\\\)[^\s<>"|?*]*/g;
const LOG_RE = new RegExp(`${URL_RE.source}|${PATH_RE.source}`, 'g');

// Escapes HTML, then marks http(s) URLs and absolute paths as openable links.
// Trailing punctuation stays outside the link because log lines are prose.
export function linkifyLog(text) {
    return escapeHtml(text).replace(LOG_RE, (m) => {
        const {core, tail} = splitTrailing(m, /[.,;:!?)\]}>'"`]+$/);
        if (/^https?:\/\//.test(core)) {
            return `<a class="ext-link" data-url="${core}" title="Ctrl+单击在浏览器打开">${core}</a>${tail}`;
        }
        if (core.replace(/[\\/]+$/, '').length < 3) return m;
        return `<a class="ext-link ext-path" data-path="${core}" title="Ctrl+单击打开，右键查看所在目录">${core}</a>${tail}`;
    });
}

function splitTrailing(text, re) {
    const match = text.match(re);
    if (!match) return {core: text, tail: ''};
    return {core: text.slice(0, text.length - match[0].length), tail: match[0]};
}

export function highlightCommand(cmd) {
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

export function inferName(command) {
    if (!command) return '';
    const parts = command.split(/\s+/);
    return parts[0] || '';
}


export function executableCommand(path, engine) {
    if (engine === 'cmd') return `"${path}"`;
    return `& '${path.replace(/'/g, "''")}'`;
}
