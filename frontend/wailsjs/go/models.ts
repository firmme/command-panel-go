export namespace main {

	export class CommandVariable {
	    name: string;
	    value?: string;
	    protectedValue?: string;

	    static createFrom(source: any = {}) {
	        return new CommandVariable(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.value = source["value"];
	        this.protectedValue = source["protectedValue"];
	    }
	}
	export class Command {
	    id: string;
	    name: string;
	    command: string;
	    cwd: string;
	    engine?: string;
	    note: string;
	    autoStart: boolean;
	    order: number;
	    variables?: CommandVariable[];
	    archived: boolean;
	    archivedAt: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;

	    static createFrom(source: any = {}) {
	        return new Command(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.command = source["command"];
	        this.cwd = source["cwd"];
	        this.engine = source["engine"];
	        this.note = source["note"];
	        this.autoStart = source["autoStart"];
	        this.order = source["order"];
	        this.variables = this.convertValues(source["variables"], CommandVariable);
	        this.archived = source["archived"];
	        this.archivedAt = source["archivedAt"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class ExecutablePick {
	    path: string;
	    dir: string;

	    static createFrom(source: any = {}) {
	        return new ExecutablePick(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.dir = source["dir"];
	    }
	}
	export class LogChunk {
	    text: string;
	    offset: number;
	    version: string;
	    reset: boolean;

	    static createFrom(source: any = {}) {
	        return new LogChunk(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.offset = source["offset"];
	        this.version = source["version"];
	        this.reset = source["reset"];
	    }
	}
	export class ManagedCommand {
	    id: string;
	    command: string;
	    cwd: string;
	    name: string;

	    static createFrom(source: any = {}) {
	        return new ManagedCommand(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.command = source["command"];
	        this.cwd = source["cwd"];
	        this.name = source["name"];
	    }
	}
	export class RunState {
	    cmd?: ManagedCommand;
	    pid: number;
	    // Go type: time
	    startTime: any;
	    // Go type: time
	    endTime: any;
	    exitCode: number;
	    running: boolean;
	    stopped: boolean;
	    duration: string;
	    logPath: string;
	    logSize: number;

	    static createFrom(source: any = {}) {
	        return new RunState(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cmd = this.convertValues(source["cmd"], ManagedCommand);
	        this.pid = source["pid"];
	        this.startTime = this.convertValues(source["startTime"], null);
	        this.endTime = this.convertValues(source["endTime"], null);
	        this.exitCode = source["exitCode"];
	        this.running = source["running"];
	        this.stopped = source["stopped"];
	        this.duration = source["duration"];
	        this.logPath = source["logPath"];
	        this.logSize = source["logSize"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RunStateSnapshot {
	    states: Record<string, RunState>;

	    static createFrom(source: any = {}) {
	        return new RunStateSnapshot(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.states = this.convertValues(source["states"], RunState, true);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

