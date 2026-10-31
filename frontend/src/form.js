import {GetArchivedCommands, AddCommand, UpdateCommand} from '../wailsjs/go/main/App.js';

export function createCommandForm({getCommands, icon, showToast, onSaved}) {
    const commandForm = document.getElementById('command-form');
    const formTitle = document.getElementById('form-title');
    const formOverlay = document.getElementById('form-overlay');
    let saving = false;
    function showFormModal() { formOverlay.style.display = 'flex'; }
    function hideFormModal() { formOverlay.style.display = 'none'; }
    // Edit command
    function editCommand(id) {
        const cmd = getCommands().find(c => c.id === id);
        if (!cmd) {
            GetArchivedCommands().then(archived => {
                const aCmd = (archived || []).find(c => c.id === id);
                if (aCmd) fillForm(aCmd);
            }).catch(e => showToast("读取归档失败: " + e, true));
            return;
        }
        fillForm(cmd);
    }

    function fillForm(cmd) {
        formTitle.textContent = '编辑命令';
        document.getElementById('cmd-id').value = cmd.id;
        document.getElementById('cmd-name').value = cmd.name || '';
        document.getElementById('cmd-command').value = cmd.command || '';
        document.getElementById('cmd-cwd').value = cmd.cwd || '';
        document.getElementById('cmd-engine').value = cmd.engine || '';
        document.getElementById('cmd-note').value = cmd.note || '';
        document.getElementById('cmd-autostart').checked = cmd.autoStart || false;
        varValues = Object.create(null);
        removedVars = new Set();
        (cmd.variables || []).forEach(v => {
            varValues[v.name] = v.value;
        });
        rebuildVarRows();
        showFormModal();
    }

    // ── 敏感变量:命令中的 ${名称} 占位符 ─────────────
    let varValues = Object.create(null);
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
        if (saving) return;
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

        saving = true;
        const saveButton = document.getElementById('btn-save');
        saveButton.disabled = true;
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
            await onSaved();
        } catch (e) {
            showToast('保存失败: ' + e, true);
        } finally { saving = false; saveButton.disabled = false; }
    }

    // Reset form
    function resetForm() {
        commandForm.reset();
        document.getElementById('cmd-id').value = '';
        varValues = Object.create(null);
        removedVars = new Set();
        document.getElementById('var-group').style.display = 'none';
        document.getElementById('var-container').innerHTML = '';
    }

    return {showFormModal, hideFormModal, editCommand, rebuildVarRows, saveCommand, resetForm};
}
