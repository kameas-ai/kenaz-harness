export namespace a2a {
	
	export class Card {
	    id: string;
	    issuer: string;
	    subject: string;
	    issuedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Card(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.issuer = source["issuer"];
	        this.subject = source["subject"];
	        this.issuedAt = source["issuedAt"];
	    }
	}

}

export namespace audit {
	
	export class Entry {
	    id: string;
	    timestamp: string;
	    category: string;
	    subject: string;
	    trailing?: string;
	
	    static createFrom(source: any = {}) {
	        return new Entry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.timestamp = source["timestamp"];
	        this.category = source["category"];
	        this.subject = source["subject"];
	        this.trailing = source["trailing"];
	    }
	}
	export class Filter {
	    categories?: string[];
	    since?: string;
	    until?: string;
	    limit?: number;
	
	    static createFrom(source: any = {}) {
	        return new Filter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.categories = source["categories"];
	        this.since = source["since"];
	        this.until = source["until"];
	        this.limit = source["limit"];
	    }
	}

}

export namespace bundle {
	
	export class Artifact {
	    name: string;
	    kind: string;
	    contentHash: string;
	
	    static createFrom(source: any = {}) {
	        return new Artifact(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.contentHash = source["contentHash"];
	    }
	}
	export class Bundle {
	    id: string;
	    name: string;
	    version: string;
	    tier: string;
	    source?: string;
	    signature?: string;
	    artifactCount: number;
	    artifacts?: Artifact[];
	
	    static createFrom(source: any = {}) {
	        return new Bundle(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.version = source["version"];
	        this.tier = source["tier"];
	        this.source = source["source"];
	        this.signature = source["signature"];
	        this.artifactCount = source["artifactCount"];
	        this.artifacts = this.convertValues(source["artifacts"], Artifact);
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

export namespace contextview {
	
	export class ContextEntry {
	    id: string;
	    kind: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new ContextEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.label = source["label"];
	    }
	}

}

export namespace llm {
	
	export class CredentialReference {
	    kind: string;
	    locator: string;
	
	    static createFrom(source: any = {}) {
	        return new CredentialReference(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.locator = source["locator"];
	    }
	}
	export class AddProviderInput {
	    id: string;
	    name: string;
	    kind: string;
	    model: string;
	    region?: string;
	    cred: CredentialReference;
	    plaintextApiKey?: string;
	
	    static createFrom(source: any = {}) {
	        return new AddProviderInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.model = source["model"];
	        this.region = source["region"];
	        this.cred = this.convertValues(source["cred"], CredentialReference);
	        this.plaintextApiKey = source["plaintextApiKey"];
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
	
	export class ModelInfo {
	    id: string;
	    displayName: string;
	    description?: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.description = source["description"];
	    }
	}
	export class Provider {
	    id: string;
	    name: string;
	    tier: string;
	    model: string;
	    source?: string;
	    validated?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Provider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.tier = source["tier"];
	        this.model = source["model"];
	        this.source = source["source"];
	        this.validated = source["validated"];
	    }
	}
	export class TestResult {
	    success: boolean;
	    latency_ms: number;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.latency_ms = source["latency_ms"];
	        this.message = source["message"];
	    }
	}

}

export namespace mcp {
	
	export class Server {
	    id: string;
	    name: string;
	    state: string;
	    version: string;
	    transport?: string;
	    capabilities?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Server(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.state = source["state"];
	        this.version = source["version"];
	        this.transport = source["transport"];
	        this.capabilities = source["capabilities"];
	    }
	}

}

export namespace policy {
	
	export class Denial {
	    policyId: string;
	    clauseId: string;
	    violatingInput: string;
	    remediation: string;
	
	    static createFrom(source: any = {}) {
	        return new Denial(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.policyId = source["policyId"];
	        this.clauseId = source["clauseId"];
	        this.violatingInput = source["violatingInput"];
	        this.remediation = source["remediation"];
	    }
	}

}

export namespace rpc {
	
	export class WindowSize {
	    width: number;
	    height: number;
	
	    static createFrom(source: any = {}) {
	        return new WindowSize(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.width = source["width"];
	        this.height = source["height"];
	    }
	}
	export class AppInfo {
	    build: string;
	    commit: string;
	    buildTime: string;
	    goVersion: string;
	    platform: string;
	    windowSize: WindowSize;
	
	    static createFrom(source: any = {}) {
	        return new AppInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.build = source["build"];
	        this.commit = source["commit"];
	        this.buildTime = source["buildTime"];
	        this.goVersion = source["goVersion"];
	        this.platform = source["platform"];
	        this.windowSize = this.convertValues(source["windowSize"], WindowSize);
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
	export class ShellStatus {
	    activeProvider: string;
	    trustTier: string;
	    harnessBuild: string;
	    connection: string;
	    eventRate: number;
	    policyApplied: boolean;
	    redactionOn: boolean;
	    localFirstOn: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ShellStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.activeProvider = source["activeProvider"];
	        this.trustTier = source["trustTier"];
	        this.harnessBuild = source["harnessBuild"];
	        this.connection = source["connection"];
	        this.eventRate = source["eventRate"];
	        this.policyApplied = source["policyApplied"];
	        this.redactionOn = source["redactionOn"];
	        this.localFirstOn = source["localFirstOn"];
	    }
	}

}

export namespace sessions {
	
	export class ToolCall {
	    id: string;
	    name: string;
	    argsSummary: string;
	    latency?: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolCall(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.argsSummary = source["argsSummary"];
	        this.latency = source["latency"];
	    }
	}
	export class Message {
	    id: string;
	    sessionId: string;
	    role: string;
	    content: string;
	    createdAt: string;
	    streaming?: boolean;
	    toolCalls?: ToolCall[];
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.role = source["role"];
	        this.content = source["content"];
	        this.createdAt = source["createdAt"];
	        this.streaming = source["streaming"];
	        this.toolCalls = this.convertValues(source["toolCalls"], ToolCall);
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
	export class Session {
	    id: string;
	    name: string;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}

}

export namespace settings {
	
	export class WindowSize {
	    width: number;
	    height: number;
	
	    static createFrom(source: any = {}) {
	        return new WindowSize(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.width = source["width"];
	        this.height = source["height"];
	    }
	}
	export class Settings {
	    schemaVersion: number;
	    lastRoute: string;
	    theme: string;
	    accent: string;
	    windowSize: WindowSize;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schemaVersion = source["schemaVersion"];
	        this.lastRoute = source["lastRoute"];
	        this.theme = source["theme"];
	        this.accent = source["accent"];
	        this.windowSize = this.convertValues(source["windowSize"], WindowSize);
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

export namespace trust {
	
	export class SecretReference {
	    id: string;
	    label: string;
	    source: string;
	    createdAt: string;
	
	    static createFrom(source: any = {}) {
	        return new SecretReference(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.source = source["source"];
	        this.createdAt = source["createdAt"];
	    }
	}

}

export namespace workflow {
	
	export class Job {
	    id: string;
	    name: string;
	    state: string;
	    startedAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new Job(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.state = source["state"];
	        this.startedAt = source["startedAt"];
	    }
	}

}

