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

export namespace artifacts {

	export class ArtifactSourceRef {
	    messageId?: string;
	    toolCallId?: string;
	    codeBlockIndex?: number;
	    filename?: string;

	    static createFrom(source: any = {}) {
	        return new ArtifactSourceRef(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.messageId = source["messageId"];
	        this.toolCallId = source["toolCallId"];
	        this.codeBlockIndex = source["codeBlockIndex"];
	        this.filename = source["filename"];
	    }
	}
	export class Artifact {
	    id: string;
	    sessionId: string;
	    projectId?: string;
	    title: string;
	    mimeType: string;
	    contentHash: string;
	    byteSize: number;
	    source: string;
	    sourceRef: ArtifactSourceRef;
	    scopeKind: string;
	    createdAt: string;

	    static createFrom(source: any = {}) {
	        return new Artifact(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.projectId = source["projectId"];
	        this.title = source["title"];
	        this.mimeType = source["mimeType"];
	        this.contentHash = source["contentHash"];
	        this.byteSize = source["byteSize"];
	        this.source = source["source"];
	        this.sourceRef = this.convertValues(source["sourceRef"], ArtifactSourceRef);
	        this.scopeKind = source["scopeKind"];
	        this.createdAt = source["createdAt"];
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
	export class ArtifactFilter {
	    sessionId?: string;
	    projectId?: string;
	    mimeTypePrefix?: string;
	    source?: string;
	    scopeKind?: string;

	    static createFrom(source: any = {}) {
	        return new ArtifactFilter(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.projectId = source["projectId"];
	        this.mimeTypePrefix = source["mimeTypePrefix"];
	        this.source = source["source"];
	        this.scopeKind = source["scopeKind"];
	    }
	}

	export class ArtifactWithBytes {
	    artifact: Artifact;
	    bytes: number[];

	    static createFrom(source: any = {}) {
	        return new ArtifactWithBytes(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.artifact = this.convertValues(source["artifact"], Artifact);
	        this.bytes = source["bytes"];
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

export namespace attachments {

	export class AddInput {
	    scopeKind: string;
	    scopeId?: string;
	    contentSource: string;
	    content: string;
	    kind?: string;
	    position?: number;
	
	    static createFrom(source: any = {}) {
	        return new AddInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scopeKind = source["scopeKind"];
	        this.scopeId = source["scopeId"];
	        this.contentSource = source["contentSource"];
	        this.content = source["content"];
	        this.kind = source["kind"];
	        this.position = source["position"];
	    }
	}
	export class AddMediaInput {
	    scopeKind: string;
	    scopeId?: string;
	    mediaBytesBase64: string;
	    mediaType: string;
	    originalName?: string;
	
	    static createFrom(source: any = {}) {
	        return new AddMediaInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scopeKind = source["scopeKind"];
	        this.scopeId = source["scopeId"];
	        this.mediaBytesBase64 = source["mediaBytesBase64"];
	        this.mediaType = source["mediaType"];
	        this.originalName = source["originalName"];
	    }
	}
	export class Attachment {
	    id: string;
	    scopeKind: string;
	    scopeId?: string;
	    contentSource: string;
	    content: string;
	    kind: string;
	    position: number;
	    createdAt: string;
	    mediaId?: string;
	
	    static createFrom(source: any = {}) {
	        return new Attachment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.scopeKind = source["scopeKind"];
	        this.scopeId = source["scopeId"];
	        this.contentSource = source["contentSource"];
	        this.content = source["content"];
	        this.kind = source["kind"];
	        this.position = source["position"];
	        this.createdAt = source["createdAt"];
	        this.mediaId = source["mediaId"];
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

export namespace contexts {
	
	export class Node {
	    name: string;
	    path: string;
	    kind: string;
	    size?: number;
	    // Go type: time
	    modified?: any;
	    children?: Node[];
	
	    static createFrom(source: any = {}) {
	        return new Node(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.kind = source["kind"];
	        this.size = source["size"];
	        this.modified = this.convertValues(source["modified"], null);
	        this.children = this.convertValues(source["children"], Node);
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

export namespace hooks {
	
	export class BuiltinDescriptor {
	    id: string;
	    name: string;
	    description: string;
	    events: string[];
	    defaultConfig?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new BuiltinDescriptor(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.events = source["events"];
	        this.defaultConfig = source["defaultConfig"];
	    }
	}
	export class Match {
	    sessionIds?: string[];
	    kinds?: string[];
	    models?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Match(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionIds = source["sessionIds"];
	        this.kinds = source["kinds"];
	        this.models = source["models"];
	    }
	}
	export class Hook {
	    id: string;
	    name: string;
	    event: string;
	    kind: string;
	    enabled: boolean;
	    match: Match;
	    builtin?: string;
	    command?: string;
	    mcpTool?: string;
	    config?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new Hook(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.event = source["event"];
	        this.kind = source["kind"];
	        this.enabled = source["enabled"];
	        this.match = this.convertValues(source["match"], Match);
	        this.builtin = source["builtin"];
	        this.command = source["command"];
	        this.mcpTool = source["mcpTool"];
	        this.config = source["config"];
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
	    models?: string[];
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
	        this.models = source["models"];
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
	export class ToolResult {
	    tool_use_id?: string;
	    content?: number[];
	    is_error?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ToolResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tool_use_id = source["tool_use_id"];
	        this.content = source["content"];
	        this.is_error = source["is_error"];
	    }
	}
	export class ToolUse {
	    id: string;
	    name: string;
	    input: number[];
	
	    static createFrom(source: any = {}) {
	        return new ToolUse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.input = source["input"];
	    }
	}
	export class MediaSource {
	    kind: string;
	    media_type: string;
	    data?: string;
	    uri?: string;
	    original_name?: string;
	
	    static createFrom(source: any = {}) {
	        return new MediaSource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.media_type = source["media_type"];
	        this.data = source["data"];
	        this.uri = source["uri"];
	        this.original_name = source["original_name"];
	    }
	}
	export class ContentBlock {
	    type: string;
	    text?: string;
	    source?: MediaSource;
	    tool_use?: ToolUse;
	    tool_result?: ToolResult;
	    tool_data?: number[];
	
	    static createFrom(source: any = {}) {
	        return new ContentBlock(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.text = source["text"];
	        this.source = this.convertValues(source["source"], MediaSource);
	        this.tool_use = this.convertValues(source["tool_use"], ToolUse);
	        this.tool_result = this.convertValues(source["tool_result"], ToolResult);
	        this.tool_data = source["tool_data"];
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
	    kind?: string;
	    model: string;
	    models?: string[];
	    region?: string;
	    cred?: CredentialReference;
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
	        this.kind = source["kind"];
	        this.model = source["model"];
	        this.models = source["models"];
	        this.region = source["region"];
	        this.cred = this.convertValues(source["cred"], CredentialReference);
	        this.source = source["source"];
	        this.validated = source["validated"];
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

export namespace memory {
	
	export class Chunk {
	    id: string;
	    sessionId?: string;
	    projectId?: string;
	    scopeKind: string;
	    scopeId: string;
	    sourceTurn?: string;
	    content: string;
	    contentHash: string;
	    toolName?: string;
	    filesRead?: string[];
	    filesModified?: string[];
	    title?: string;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Chunk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.projectId = source["projectId"];
	        this.scopeKind = source["scopeKind"];
	        this.scopeId = source["scopeId"];
	        this.sourceTurn = source["sourceTurn"];
	        this.content = source["content"];
	        this.contentHash = source["contentHash"];
	        this.toolName = source["toolName"];
	        this.filesRead = source["filesRead"];
	        this.filesModified = source["filesModified"];
	        this.title = source["title"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
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
	export class ListFilter {
	    scopeKind?: string;
	    scopeId?: string;
	
	    static createFrom(source: any = {}) {
	        return new ListFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scopeKind = source["scopeKind"];
	        this.scopeId = source["scopeId"];
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

export namespace projects {
	
	export class Project {
	    id: string;
	    name: string;
	    description: string;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Project(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class Session {
	    id: string;
	    name: string;
	    projectId?: string;
	    createdAt: string;
	    updatedAt: string;
	    lastActiveAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.projectId = source["projectId"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.lastActiveAt = source["lastActiveAt"];
	    }
	}

}

export namespace recipes {
	
	export class Capabilities {
	    tools: boolean;
	    resources: boolean;
	    prompts: boolean;
	    sampling: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Capabilities(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tools = source["tools"];
	        this.resources = source["resources"];
	        this.prompts = source["prompts"];
	        this.sampling = source["sampling"];
	    }
	}
	export class ConfigOption {
	    name: string;
	    display: string;
	    kind: string;
	    default?: any;
	    required: boolean;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfigOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.display = source["display"];
	        this.kind = source["kind"];
	        this.default = source["default"];
	        this.required = source["required"];
	        this.description = source["description"];
	    }
	}
	export class EnvKey {
	    name: string;
	    display: string;
	    docs_url: string;
	    required: boolean;
	
	    static createFrom(source: any = {}) {
	        return new EnvKey(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.display = source["display"];
	        this.docs_url = source["docs_url"];
	        this.required = source["required"];
	    }
	}
	export class SamplingPolicy {
	    allowed: boolean;
	    default: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SamplingPolicy(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.allowed = source["allowed"];
	        this.default = source["default"];
	    }
	}
	export class Recipe {
	    id: string;
	    display_name: string;
	    description: string;
	    category: string;
	    command: string[];
	    env_keys: EnvKey[];
	    capabilities: Capabilities;
	    docs_url: string;
	    init_timeout_ms: number;
	    ping_period_ms: number;
	    sampling_policy: SamplingPolicy;
	    args_template?: string[];
	    config_options?: ConfigOption[];
	
	    static createFrom(source: any = {}) {
	        return new Recipe(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.display_name = source["display_name"];
	        this.description = source["description"];
	        this.category = source["category"];
	        this.command = source["command"];
	        this.env_keys = this.convertValues(source["env_keys"], EnvKey);
	        this.capabilities = this.convertValues(source["capabilities"], Capabilities);
	        this.docs_url = source["docs_url"];
	        this.init_timeout_ms = source["init_timeout_ms"];
	        this.ping_period_ms = source["ping_period_ms"];
	        this.sampling_policy = this.convertValues(source["sampling_policy"], SamplingPolicy);
	        this.args_template = source["args_template"];
	        this.config_options = this.convertValues(source["config_options"], ConfigOption);
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
	export class ShellReadFileResult {
	    dataBase64: string;
	    mediaType: string;
	
	    static createFrom(source: any = {}) {
	        return new ShellReadFileResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dataBase64 = source["dataBase64"];
	        this.mediaType = source["mediaType"];
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
	    systemPrompt: string;
	    contextKind: string;
	    projectId?: string;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.systemPrompt = source["systemPrompt"];
	        this.contextKind = source["contextKind"];
	        this.projectId = source["projectId"];
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
	    memoryEnabled: boolean;
	    confirmEachDisabled: boolean;
	
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
	        this.memoryEnabled = source["memoryEnabled"];
	        this.confirmEachDisabled = source["confirmEachDisabled"];
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

export namespace slashcmd {

	export class CommandInfo {
	    name: string;
	    description: string;
	    comingSoon: boolean;

	    static createFrom(source: any = {}) {
	        return new CommandInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.comingSoon = source["comingSoon"];
	    }
	}
	export class ExecuteResult {
	    text: string;
	    kind: string;
	    metadata?: Record<string, any>;

	    static createFrom(source: any = {}) {
	        return new ExecuteResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.kind = source["kind"];
	        this.metadata = source["metadata"];
	    }
	}

}

export namespace stdio {
	
	export class RecipeStatus {
	    id: string;
	    enabled: boolean;
	    state: string;
	    last_error?: string;
	    restart_attempts: number;
	    // Go type: time
	    last_restart_at?: any;
	    keys_present: boolean;
	    pid: number;
	    protocol_version?: string;
	    server_name?: string;
	    server_version?: string;
	    tool_count: number;
	    resource_count: number;
	    prompt_count: number;
	    stderr_tail?: string;
	    // Go type: time
	    updated_at: any;
	
	    static createFrom(source: any = {}) {
	        return new RecipeStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.enabled = source["enabled"];
	        this.state = source["state"];
	        this.last_error = source["last_error"];
	        this.restart_attempts = source["restart_attempts"];
	        this.last_restart_at = this.convertValues(source["last_restart_at"], null);
	        this.keys_present = source["keys_present"];
	        this.pid = source["pid"];
	        this.protocol_version = source["protocol_version"];
	        this.server_name = source["server_name"];
	        this.server_version = source["server_version"];
	        this.tool_count = source["tool_count"];
	        this.resource_count = source["resource_count"];
	        this.prompt_count = source["prompt_count"];
	        this.stderr_tail = source["stderr_tail"];
	        this.updated_at = this.convertValues(source["updated_at"], null);
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

export namespace tools {
	
	export class RecipeListing {
	    recipe: recipes.Recipe;
	    enabled: boolean;
	    status: stdio.RecipeStatus;
	    keysPresent: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RecipeListing(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.recipe = this.convertValues(source["recipe"], recipes.Recipe);
	        this.enabled = source["enabled"];
	        this.status = this.convertValues(source["status"], stdio.RecipeStatus);
	        this.keysPresent = source["keysPresent"];
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

