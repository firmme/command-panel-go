// One shared queue for backend events, user actions and polling.
export function createRefreshQueue(refresh, onError, delay = 20) {
    let pending = false;
    let full = false;
    let current = null;

    async function drain() {
        await new Promise(resolve => setTimeout(resolve, delay));
        while (pending) {
            const includeCommands = full;
            pending = false;
            full = false;
            try { await refresh(includeCommands); }
            catch (e) { onError(e); }
        }
    }

    function request(includeCommands = false) {
        // Polling can share an active refresh; mutations require another pass
        // when a prior request might have read the old configuration.
        if (current && !includeCommands) return current;
        pending = true;
        full ||= includeCommands;
        if (!current) current = drain().finally(() => { current = null; });
        return current;
    }

    return {request};
}
