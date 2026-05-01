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

export namespace agentgraph {
	
	export class GraphInfo {
	    id: string;
	    name?: string;
	    description?: string;
	    scope: string;
	    source?: string;
	    updatedAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new GraphInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.scope = source["scope"];
	        this.source = source["source"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class GraphSpec {
	    id: string;
	    name?: string;
	    scope: string;
	    yaml: string;
	
	    static createFrom(source: any = {}) {
	        return new GraphSpec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.scope = source["scope"];
	        this.yaml = source["yaml"];
	    }
	}
	export class PendingAsk {
	    nodeId: string;
	    question: string;
	
	    static createFrom(source: any = {}) {
	        return new PendingAsk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.nodeId = source["nodeId"];
	        this.question = source["question"];
	    }
	}
	export class RunStatus {
	    runId: string;
	    graphId: string;
	    sessionId?: string;
	    state: string;
	    startedAt: string;
	    updatedAt: string;
	    completedAt?: string;
	    error?: string;
	    nodesComplete: number;
	    llmTokens: number;
	    llmCalls: number;
	    toolCalls: number;
	    costUsd: number;
	    pendingAsk?: PendingAsk;
	
	    static createFrom(source: any = {}) {
	        return new RunStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.graphId = source["graphId"];
	        this.sessionId = source["sessionId"];
	        this.state = source["state"];
	        this.startedAt = source["startedAt"];
	        this.updatedAt = source["updatedAt"];
	        this.completedAt = source["completedAt"];
	        this.error = source["error"];
	        this.nodesComplete = source["nodesComplete"];
	        this.llmTokens = source["llmTokens"];
	        this.llmCalls = source["llmCalls"];
	        this.toolCalls = source["toolCalls"];
	        this.costUsd = source["costUsd"];
	        this.pendingAsk = this.convertValues(source["pendingAsk"], PendingAsk);
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
	export class RunTraceEvent {
	    seq: number;
	    runId: string;
	    nodeId?: string;
	    kind: string;
	    ts: string;
	    payload?: string;
	
	    static createFrom(source: any = {}) {
	        return new RunTraceEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.seq = source["seq"];
	        this.runId = source["runId"];
	        this.nodeId = source["nodeId"];
	        this.kind = source["kind"];
	        this.ts = source["ts"];
	        this.payload = source["payload"];
	    }
	}
	export class StartRunRequest {
	    graphId: string;
	    sessionId?: string;
	    inputs?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new StartRunRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.graphId = source["graphId"];
	        this.sessionId = source["sessionId"];
	        this.inputs = source["inputs"];
	    }
	}
	export class StartRunResponse {
	    runId: string;
	    status: RunStatus;
	
	    static createFrom(source: any = {}) {
	        return new StartRunResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.status = this.convertValues(source["status"], RunStatus);
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
	export class ValidationIssue {
	    rule: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ValidationIssue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rule = source["rule"];
	        this.message = source["message"];
	    }
	}
	export class ValidationResult {
	    ok: boolean;
	    issues: ValidationIssue[];
	
	    static createFrom(source: any = {}) {
	        return new ValidationResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.issues = this.convertValues(source["issues"], ValidationIssue);
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

export namespace branches {
	
	export class Branch {
	    id: string;
	    parentSessionId: string;
	    childSessionId: string;
	    kind: string;
	    status: string;
	    providerId?: string;
	    modelId?: string;
	    title?: string;
	    taskHint?: string;
	    createdAt: string;
	    updatedAt: string;
	    mergedAt?: string;
	    abandonedAt?: string;
	    subagentBranch?: boolean;
	    recommendationId?: string;
	    advisorSignals?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Branch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.parentSessionId = source["parentSessionId"];
	        this.childSessionId = source["childSessionId"];
	        this.kind = source["kind"];
	        this.status = source["status"];
	        this.providerId = source["providerId"];
	        this.modelId = source["modelId"];
	        this.title = source["title"];
	        this.taskHint = source["taskHint"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.mergedAt = source["mergedAt"];
	        this.abandonedAt = source["abandonedAt"];
	        this.subagentBranch = source["subagentBranch"];
	        this.recommendationId = source["recommendationId"];
	        this.advisorSignals = source["advisorSignals"];
	    }
	}
	export class BranchStatus {
	    branch: Branch;
	    childSessionId: string;
	    hasInflightRun: boolean;
	    lastActivityAt?: string;
	    lastAssistantMessage?: string;
	
	    static createFrom(source: any = {}) {
	        return new BranchStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.branch = this.convertValues(source["branch"], Branch);
	        this.childSessionId = source["childSessionId"];
	        this.hasInflightRun = source["hasInflightRun"];
	        this.lastActivityAt = source["lastActivityAt"];
	        this.lastAssistantMessage = source["lastAssistantMessage"];
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
	export class CommitReintegrationOptions {
	    branchSessionId: string;
	    finalSummaryText: string;
	    wasEdited: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CommitReintegrationOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.branchSessionId = source["branchSessionId"];
	        this.finalSummaryText = source["finalSummaryText"];
	        this.wasEdited = source["wasEdited"];
	    }
	}
	export class CreateBranchOptions {
	    parentSessionId: string;
	    title?: string;
	    taskHint?: string;
	    modelPreference?: string;
	    exactProviderId?: string;
	    exactModelId?: string;
	    systemPromptOverride?: string;
	    childName?: string;
	    recommendationId?: string;
	    advisorSignals?: string[];
	    advisorConfidence?: number;
	
	    static createFrom(source: any = {}) {
	        return new CreateBranchOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.parentSessionId = source["parentSessionId"];
	        this.title = source["title"];
	        this.taskHint = source["taskHint"];
	        this.modelPreference = source["modelPreference"];
	        this.exactProviderId = source["exactProviderId"];
	        this.exactModelId = source["exactModelId"];
	        this.systemPromptOverride = source["systemPromptOverride"];
	        this.childName = source["childName"];
	        this.recommendationId = source["recommendationId"];
	        this.advisorSignals = source["advisorSignals"];
	        this.advisorConfidence = source["advisorConfidence"];
	    }
	}
	export class RecommendedModel {
	    providerId: string;
	    modelId: string;
	    tier: string;
	    reason: string;
	    notes?: string;
	    crossProviderWarning?: string;
	
	    static createFrom(source: any = {}) {
	        return new RecommendedModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providerId = source["providerId"];
	        this.modelId = source["modelId"];
	        this.tier = source["tier"];
	        this.reason = source["reason"];
	        this.notes = source["notes"];
	        this.crossProviderWarning = source["crossProviderWarning"];
	    }
	}
	export class ReintegrationProposal {
	    proposedSummary: string;
	    tokenCount: number;
	    model?: string;
	
	    static createFrom(source: any = {}) {
	        return new ReintegrationProposal(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.proposedSummary = source["proposedSummary"];
	        this.tokenCount = source["tokenCount"];
	        this.model = source["model"];
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

export namespace cedar {
	
	export class BashPromptSurface {
	    pattern: string;
	    argv: string[];
	    working_dir?: string;
	    dangerous: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BashPromptSurface(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pattern = source["pattern"];
	        this.argv = source["argv"];
	        this.working_dir = source["working_dir"];
	        this.dangerous = source["dangerous"];
	    }
	}
	export class CredPromptSurface {
	    provider_id: string;
	    purpose: string;
	
	    static createFrom(source: any = {}) {
	        return new CredPromptSurface(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider_id = source["provider_id"];
	        this.purpose = source["purpose"];
	    }
	}
	export class Decision {
	    outcome: number;
	    action: string;
	    principal: string;
	    resource: string;
	    matched_policy?: string;
	    reason?: string;
	    // Go type: time
	    evaluated_at: any;
	
	    static createFrom(source: any = {}) {
	        return new Decision(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.outcome = source["outcome"];
	        this.action = source["action"];
	        this.principal = source["principal"];
	        this.resource = source["resource"];
	        this.matched_policy = source["matched_policy"];
	        this.reason = source["reason"];
	        this.evaluated_at = this.convertValues(source["evaluated_at"], null);
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
	export class FSPromptSurface {
	    op: string;
	    canonical_path: string;
	    dangerous: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FSPromptSurface(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.op = source["op"];
	        this.canonical_path = source["canonical_path"];
	        this.dangerous = source["dangerous"];
	    }
	}
	export class ToolPromptSurface {
	    tool_name: string;
	    server_name?: string;
	    args_redacted?: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolPromptSurface(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tool_name = source["tool_name"];
	        this.server_name = source["server_name"];
	        this.args_redacted = source["args_redacted"];
	    }
	}
	export class PromptSurface {
	    bash?: BashPromptSurface;
	    fs?: FSPromptSurface;
	    cred?: CredPromptSurface;
	    tool?: ToolPromptSurface;
	    reason?: string;
	    session_id?: string;
	
	    static createFrom(source: any = {}) {
	        return new PromptSurface(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bash = this.convertValues(source["bash"], BashPromptSurface);
	        this.fs = this.convertValues(source["fs"], FSPromptSurface);
	        this.cred = this.convertValues(source["cred"], CredPromptSurface);
	        this.tool = this.convertValues(source["tool"], ToolPromptSurface);
	        this.reason = source["reason"];
	        this.session_id = source["session_id"];
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
	export class PendingRequest {
	    request_id: string;
	    family: string;
	    surface: PromptSurface;
	    // Go type: time
	    issued_at: any;
	    // Go type: time
	    deadline_at: any;
	
	    static createFrom(source: any = {}) {
	        return new PendingRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.request_id = source["request_id"];
	        this.family = source["family"];
	        this.surface = this.convertValues(source["surface"], PromptSurface);
	        this.issued_at = this.convertValues(source["issued_at"], null);
	        this.deadline_at = this.convertValues(source["deadline_at"], null);
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
	export class PolicyFile {
	    name: string;
	    path: string;
	    bytes: number;
	    embedded: boolean;
	    parse_ok: boolean;
	    parse_err?: string;
	
	    static createFrom(source: any = {}) {
	        return new PolicyFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.bytes = source["bytes"];
	        this.embedded = source["embedded"];
	        this.parse_ok = source["parse_ok"];
	        this.parse_err = source["parse_err"];
	    }
	}
	

}

export namespace compaction {
	
	export class SiteConfig {
	    enabled: boolean;
	    strategy?: string;
	    preCallThreshold?: number;
	    toolResultMaxBytes?: number;
	    maxRecursionDepth?: number;
	    dropOldestKeepRecentN?: number;
	    semanticClusterCount?: number;
	    summaryProvider?: string;
	    summaryModel?: string;
	    subgraphInputPort?: string;
	    subgraphOutputPort?: string;
	    customGraphId?: string;
	
	    static createFrom(source: any = {}) {
	        return new SiteConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.strategy = source["strategy"];
	        this.preCallThreshold = source["preCallThreshold"];
	        this.toolResultMaxBytes = source["toolResultMaxBytes"];
	        this.maxRecursionDepth = source["maxRecursionDepth"];
	        this.dropOldestKeepRecentN = source["dropOldestKeepRecentN"];
	        this.semanticClusterCount = source["semanticClusterCount"];
	        this.summaryProvider = source["summaryProvider"];
	        this.summaryModel = source["summaryModel"];
	        this.subgraphInputPort = source["subgraphInputPort"];
	        this.subgraphOutputPort = source["subgraphOutputPort"];
	        this.customGraphId = source["customGraphId"];
	    }
	}
	export class Config {
	    sites?: Record<string, SiteConfig>;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sites = this.convertValues(source["sites"], SiteConfig, true);
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
	export class CustomStrategy {
	    graphId: string;
	    name: string;
	    description?: string;
	
	    static createFrom(source: any = {}) {
	        return new CustomStrategy(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.graphId = source["graphId"];
	        this.name = source["name"];
	        this.description = source["description"];
	    }
	}
	export class EffectiveConfig {
	    config: Config;
	    attribution: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new EffectiveConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.config = this.convertValues(source["config"], Config);
	        this.attribution = source["attribution"];
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
	export class ManualOpts {
	    strategy?: string;
	    dropOldestKeepRecentN?: number;
	    semanticClusterCount?: number;
	    summaryProvider?: string;
	    summaryModel?: string;
	    customGraphId?: string;
	
	    static createFrom(source: any = {}) {
	        return new ManualOpts(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.strategy = source["strategy"];
	        this.dropOldestKeepRecentN = source["dropOldestKeepRecentN"];
	        this.semanticClusterCount = source["semanticClusterCount"];
	        this.summaryProvider = source["summaryProvider"];
	        this.summaryModel = source["summaryModel"];
	        this.customGraphId = source["customGraphId"];
	    }
	}
	export class ManualResult {
	    strategy: string;
	    bytesSaved: number;
	    skipped?: boolean;
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new ManualResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.strategy = source["strategy"];
	        this.bytesSaved = source["bytesSaved"];
	        this.skipped = source["skipped"];
	        this.reason = source["reason"];
	    }
	}
	export class ScopeKey {
	    projectId?: string;
	    sessionId?: string;
	    runId?: string;
	    nodeId?: string;
	
	    static createFrom(source: any = {}) {
	        return new ScopeKey(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.sessionId = source["sessionId"];
	        this.runId = source["runId"];
	        this.nodeId = source["nodeId"];
	    }
	}
	
	export class TierExplain {
	    aggressiveness: string;
	    label: string;
	    description: string;
	    triggerPct: number;
	    summarizePct: number;
	    mode: string;
	
	    static createFrom(source: any = {}) {
	        return new TierExplain(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.aggressiveness = source["aggressiveness"];
	        this.label = source["label"];
	        this.description = source["description"];
	        this.triggerPct = source["triggerPct"];
	        this.summarizePct = source["summarizePct"];
	        this.mode = source["mode"];
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

export namespace corpus {
	
	export class ChunkProvenance {
	    filePath: string;
	    lineStart: number;
	    lineEnd: number;
	    sha256: string;
	
	    static createFrom(source: any = {}) {
	        return new ChunkProvenance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filePath = source["filePath"];
	        this.lineStart = source["lineStart"];
	        this.lineEnd = source["lineEnd"];
	        this.sha256 = source["sha256"];
	    }
	}
	export class Chunk {
	    id: string;
	    corpusId: string;
	    fileId: string;
	    chunkSeq: number;
	    text: string;
	    provenance: ChunkProvenance;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Chunk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.corpusId = source["corpusId"];
	        this.fileId = source["fileId"];
	        this.chunkSeq = source["chunkSeq"];
	        this.text = source["text"];
	        this.provenance = this.convertValues(source["provenance"], ChunkProvenance);
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
	
	export class Corpus {
	    id: string;
	    name: string;
	    scope: string;
	    scopeId?: string;
	    tag?: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Corpus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.scope = source["scope"];
	        this.scopeId = source["scopeId"];
	        this.tag = source["tag"];
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
	export class CorpusFile {
	    id: string;
	    corpusId: string;
	    path: string;
	    sha256: string;
	    fileSize: number;
	    lineCount: number;
	    // Go type: time
	    ingestedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new CorpusFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.corpusId = source["corpusId"];
	        this.path = source["path"];
	        this.sha256 = source["sha256"];
	        this.fileSize = source["fileSize"];
	        this.lineCount = source["lineCount"];
	        this.ingestedAt = this.convertValues(source["ingestedAt"], null);
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
	export class CreateRequest {
	    name: string;
	    scope: string;
	    scopeId?: string;
	    tag?: string;
	
	    static createFrom(source: any = {}) {
	        return new CreateRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.scope = source["scope"];
	        this.scopeId = source["scopeId"];
	        this.tag = source["tag"];
	    }
	}
	export class IngestOptions {
	    recursive?: boolean;
	    extensions?: string[];
	    maxFileBytes?: number;
	    chunkLines?: number;
	
	    static createFrom(source: any = {}) {
	        return new IngestOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.recursive = source["recursive"];
	        this.extensions = source["extensions"];
	        this.maxFileBytes = source["maxFileBytes"];
	        this.chunkLines = source["chunkLines"];
	    }
	}
	export class IngestStatus {
	    jobId: string;
	    corpusId: string;
	    state: string;
	    path: string;
	    filesTotal: number;
	    filesDone: number;
	    filesSkip: number;
	    chunksTotal: number;
	    // Go type: time
	    startedAt: any;
	    // Go type: time
	    updatedAt: any;
	    // Go type: time
	    finishedAt?: any;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new IngestStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.jobId = source["jobId"];
	        this.corpusId = source["corpusId"];
	        this.state = source["state"];
	        this.path = source["path"];
	        this.filesTotal = source["filesTotal"];
	        this.filesDone = source["filesDone"];
	        this.filesSkip = source["filesSkip"];
	        this.chunksTotal = source["chunksTotal"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	        this.finishedAt = this.convertValues(source["finishedAt"], null);
	        this.error = source["error"];
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
	export class RetrievalResult {
	    chunk: Chunk;
	    similarity: number;
	
	    static createFrom(source: any = {}) {
	        return new RetrievalResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.chunk = this.convertValues(source["chunk"], Chunk);
	        this.similarity = source["similarity"];
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
	export class RetrieveRequest {
	    query: string;
	    topK?: number;
	    tokenBudget?: number;
	    tag?: string;
	    scope?: string;
	
	    static createFrom(source: any = {}) {
	        return new RetrieveRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.query = source["query"];
	        this.topK = source["topK"];
	        this.tokenBudget = source["tokenBudget"];
	        this.tag = source["tag"];
	        this.scope = source["scope"];
	    }
	}
	export class RetrieveResponse {
	    results: RetrievalResult[];
	    dropped: number;
	
	    static createFrom(source: any = {}) {
	        return new RetrieveResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.results = this.convertValues(source["results"], RetrievalResult);
	        this.dropped = source["dropped"];
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

export namespace dials {
	
	export class DialConfig {
	    maxTokensPerRun?: number;
	    maxTokensPerRunSet?: boolean;
	    maxWallclockSeconds?: number;
	    maxWallclockSet?: boolean;
	    maxLLMCalls?: number;
	    maxLLMCallsSet?: boolean;
	    maxToolCalls?: number;
	    maxToolCallsSet?: boolean;
	    maxCostUSD?: number;
	    maxCostUSDSet?: boolean;
	    planVerbosity?: string;
	    planVerbositySet?: boolean;
	    askThreshold?: number;
	    askThresholdSet?: boolean;
	    reflectFrequency?: number;
	    reflectFrequencySet?: boolean;
	    compactionAggressiveness?: number;
	    compactionAggressivenessSet?: boolean;
	    reviewIterationsCap?: number;
	    reviewIterationsCapSet?: boolean;
	    memoryHooksEnabled?: boolean;
	    memoryHooksEnabledSet?: boolean;
	    memoryPruneIntervalSeconds?: number;
	    memoryPruneIntervalSet?: boolean;
	    // Go type: time
	    updatedAt?: any;
	
	    static createFrom(source: any = {}) {
	        return new DialConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.maxTokensPerRun = source["maxTokensPerRun"];
	        this.maxTokensPerRunSet = source["maxTokensPerRunSet"];
	        this.maxWallclockSeconds = source["maxWallclockSeconds"];
	        this.maxWallclockSet = source["maxWallclockSet"];
	        this.maxLLMCalls = source["maxLLMCalls"];
	        this.maxLLMCallsSet = source["maxLLMCallsSet"];
	        this.maxToolCalls = source["maxToolCalls"];
	        this.maxToolCallsSet = source["maxToolCallsSet"];
	        this.maxCostUSD = source["maxCostUSD"];
	        this.maxCostUSDSet = source["maxCostUSDSet"];
	        this.planVerbosity = source["planVerbosity"];
	        this.planVerbositySet = source["planVerbositySet"];
	        this.askThreshold = source["askThreshold"];
	        this.askThresholdSet = source["askThresholdSet"];
	        this.reflectFrequency = source["reflectFrequency"];
	        this.reflectFrequencySet = source["reflectFrequencySet"];
	        this.compactionAggressiveness = source["compactionAggressiveness"];
	        this.compactionAggressivenessSet = source["compactionAggressivenessSet"];
	        this.reviewIterationsCap = source["reviewIterationsCap"];
	        this.reviewIterationsCapSet = source["reviewIterationsCapSet"];
	        this.memoryHooksEnabled = source["memoryHooksEnabled"];
	        this.memoryHooksEnabledSet = source["memoryHooksEnabledSet"];
	        this.memoryPruneIntervalSeconds = source["memoryPruneIntervalSeconds"];
	        this.memoryPruneIntervalSet = source["memoryPruneIntervalSet"];
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
	export class DialDelta {
	    addTokensPerRun?: number;
	    addWallclockSeconds?: number;
	    addLLMCalls?: number;
	    addToolCalls?: number;
	    addCostUSD?: number;
	
	    static createFrom(source: any = {}) {
	        return new DialDelta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.addTokensPerRun = source["addTokensPerRun"];
	        this.addWallclockSeconds = source["addWallclockSeconds"];
	        this.addLLMCalls = source["addLLMCalls"];
	        this.addToolCalls = source["addToolCalls"];
	        this.addCostUSD = source["addCostUSD"];
	    }
	}
	export class EffectiveField_bool_ {
	    value: boolean;
	    from: string;
	
	    static createFrom(source: any = {}) {
	        return new EffectiveField_bool_(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.value = source["value"];
	        this.from = source["from"];
	    }
	}
	export class EffectiveField_string_ {
	    value: string;
	    from: string;
	
	    static createFrom(source: any = {}) {
	        return new EffectiveField_string_(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.value = source["value"];
	        this.from = source["from"];
	    }
	}
	export class EffectiveField_float64_ {
	    value: number;
	    from: string;
	
	    static createFrom(source: any = {}) {
	        return new EffectiveField_float64_(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.value = source["value"];
	        this.from = source["from"];
	    }
	}
	export class EffectiveField_int_ {
	    value: number;
	    from: string;
	
	    static createFrom(source: any = {}) {
	        return new EffectiveField_int_(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.value = source["value"];
	        this.from = source["from"];
	    }
	}
	export class EffectiveDials {
	    maxTokensPerRun: EffectiveField_int_;
	    maxWallclockSeconds: EffectiveField_int_;
	    maxLLMCalls: EffectiveField_int_;
	    maxToolCalls: EffectiveField_int_;
	    maxCostUSD: EffectiveField_float64_;
	    planVerbosity: EffectiveField_string_;
	    askThreshold: EffectiveField_float64_;
	    reflectFrequency: EffectiveField_int_;
	    compactionAggressiveness: EffectiveField_float64_;
	    reviewIterationsCap: EffectiveField_int_;
	    memoryHooksEnabled: EffectiveField_bool_;
	    memoryPruneIntervalSeconds: EffectiveField_int_;
	
	    static createFrom(source: any = {}) {
	        return new EffectiveDials(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.maxTokensPerRun = this.convertValues(source["maxTokensPerRun"], EffectiveField_int_);
	        this.maxWallclockSeconds = this.convertValues(source["maxWallclockSeconds"], EffectiveField_int_);
	        this.maxLLMCalls = this.convertValues(source["maxLLMCalls"], EffectiveField_int_);
	        this.maxToolCalls = this.convertValues(source["maxToolCalls"], EffectiveField_int_);
	        this.maxCostUSD = this.convertValues(source["maxCostUSD"], EffectiveField_float64_);
	        this.planVerbosity = this.convertValues(source["planVerbosity"], EffectiveField_string_);
	        this.askThreshold = this.convertValues(source["askThreshold"], EffectiveField_float64_);
	        this.reflectFrequency = this.convertValues(source["reflectFrequency"], EffectiveField_int_);
	        this.compactionAggressiveness = this.convertValues(source["compactionAggressiveness"], EffectiveField_float64_);
	        this.reviewIterationsCap = this.convertValues(source["reviewIterationsCap"], EffectiveField_int_);
	        this.memoryHooksEnabled = this.convertValues(source["memoryHooksEnabled"], EffectiveField_bool_);
	        this.memoryPruneIntervalSeconds = this.convertValues(source["memoryPruneIntervalSeconds"], EffectiveField_int_);
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
	
	
	
	
	export class ScopeKey {
	    scope: string;
	    id?: string;
	
	    static createFrom(source: any = {}) {
	        return new ScopeKey(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scope = source["scope"];
	        this.id = source["id"];
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
	export class Redacted {
	    display: string;
	    kind: string;
	    locator: string;
	
	    static createFrom(source: any = {}) {
	        return new Redacted(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.display = source["display"];
	        this.kind = source["kind"];
	        this.locator = source["locator"];
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
	    redaction?: Redacted;
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
	        this.redaction = this.convertValues(source["redaction"], Redacted);
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
	
	export class ImportRequest {
	    raw_json: string;
	    dry_run: boolean;
	    keep_ids?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ImportRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.raw_json = source["raw_json"];
	        this.dry_run = source["dry_run"];
	        this.keep_ids = source["keep_ids"];
	    }
	}
	export class ImportWrotePath {
	    id: string;
	    yaml_path: string;
	    json_path: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportWrotePath(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.yaml_path = source["yaml_path"];
	        this.json_path = source["json_path"];
	    }
	}
	export class ImportResponse {
	    report: recipes.TranslationReport;
	    wrote_paths?: ImportWrotePath[];
	
	    static createFrom(source: any = {}) {
	        return new ImportResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.report = this.convertValues(source["report"], recipes.TranslationReport);
	        this.wrote_paths = this.convertValues(source["wrote_paths"], ImportWrotePath);
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
	export class TestResult {
	    ok: boolean;
	    serverName: string;
	    serverVersion: string;
	    protocolVersion: string;
	    toolCount: number;
	    resourceCount: number;
	    promptCount: number;
	    stderrTail: string;
	    errorMessage: string;
	    durationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.serverName = source["serverName"];
	        this.serverVersion = source["serverVersion"];
	        this.protocolVersion = source["protocolVersion"];
	        this.toolCount = source["toolCount"];
	        this.resourceCount = source["resourceCount"];
	        this.promptCount = source["promptCount"];
	        this.stderrTail = source["stderrTail"];
	        this.errorMessage = source["errorMessage"];
	        this.durationMs = source["durationMs"];
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
	    pinned?: boolean;
	    recallCount?: number;
	    // Go type: time
	    lastAccessed?: any;
	    source?: string;
	
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
	        this.pinned = source["pinned"];
	        this.recallCount = source["recallCount"];
	        this.lastAccessed = this.convertValues(source["lastAccessed"], null);
	        this.source = source["source"];
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
	export class JournalEntry {
	    seq: number;
	    boundary: string;
	    scope: string;
	    title?: string;
	    source?: string;
	    written: boolean;
	    deduped: boolean;
	    skipped: boolean;
	    skipReason?: string;
	    chunkId?: string;
	    contentHash?: string;
	    // Go type: time
	    at: any;
	
	    static createFrom(source: any = {}) {
	        return new JournalEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.seq = source["seq"];
	        this.boundary = source["boundary"];
	        this.scope = source["scope"];
	        this.title = source["title"];
	        this.source = source["source"];
	        this.written = source["written"];
	        this.deduped = source["deduped"];
	        this.skipped = source["skipped"];
	        this.skipReason = source["skipReason"];
	        this.chunkId = source["chunkId"];
	        this.contentHash = source["contentHash"];
	        this.at = this.convertValues(source["at"], null);
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
	export class PruneStats {
	    // Go type: time
	    startedAt: any;
	    durationMs: number;
	    kept: number;
	    dropped: number;
	    collapsed: number;
	    pinned: number;
	
	    static createFrom(source: any = {}) {
	        return new PruneStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.durationMs = source["durationMs"];
	        this.kept = source["kept"];
	        this.dropped = source["dropped"];
	        this.collapsed = source["collapsed"];
	        this.pinned = source["pinned"];
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
	export class PruneVerdict {
	    id: string;
	    action: string;
	    reason?: string;
	    keepScore: number;
	    collapsedInto?: string;
	
	    static createFrom(source: any = {}) {
	        return new PruneVerdict(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.action = source["action"];
	        this.reason = source["reason"];
	        this.keepScore = source["keepScore"];
	        this.collapsedInto = source["collapsedInto"];
	    }
	}
	export class PrunePreview {
	    verdicts: PruneVerdict[];
	    stats: PruneStats;
	
	    static createFrom(source: any = {}) {
	        return new PrunePreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.verdicts = this.convertValues(source["verdicts"], PruneVerdict);
	        this.stats = this.convertValues(source["stats"], PruneStats);
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

export namespace nodes {
	
	export class AttrProvenance {
	    fieldPath: string;
	    layer: string;
	
	    static createFrom(source: any = {}) {
	        return new AttrProvenance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fieldPath = source["fieldPath"];
	        this.layer = source["layer"];
	    }
	}
	export class AttrSpecWire {
	    name: string;
	    type: string;
	    required?: boolean;
	    default?: any;
	    enum?: string[];
	    min?: number;
	    max?: number;
	    minLength?: number;
	    maxLength?: number;
	    description?: string;
	
	    static createFrom(source: any = {}) {
	        return new AttrSpecWire(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.required = source["required"];
	        this.default = source["default"];
	        this.enum = source["enum"];
	        this.min = source["min"];
	        this.max = source["max"];
	        this.minLength = source["minLength"];
	        this.maxLength = source["maxLength"];
	        this.description = source["description"];
	    }
	}
	export class DoctorReport {
	    shippedCount: number;
	    userOverrideCount: number;
	    archetypeCount: number;
	    callableCount: number;
	    aliasCount: number;
	    userDir?: string;
	    hotReloadEnabled: boolean;
	    lastReloadAt?: string;
	    userOverrideErrors?: string[];
	    sunsetVersion?: string;
	
	    static createFrom(source: any = {}) {
	        return new DoctorReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.shippedCount = source["shippedCount"];
	        this.userOverrideCount = source["userOverrideCount"];
	        this.archetypeCount = source["archetypeCount"];
	        this.callableCount = source["callableCount"];
	        this.aliasCount = source["aliasCount"];
	        this.userDir = source["userDir"];
	        this.hotReloadEnabled = source["hotReloadEnabled"];
	        this.lastReloadAt = source["lastReloadAt"];
	        this.userOverrideErrors = source["userOverrideErrors"];
	        this.sunsetVersion = source["sunsetVersion"];
	    }
	}
	export class PortSpecWire {
	    name: string;
	    type: string;
	    description?: string;
	    defaultFor?: string;
	
	    static createFrom(source: any = {}) {
	        return new PortSpecWire(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.description = source["description"];
	        this.defaultFor = source["defaultFor"];
	    }
	}
	export class PortSetWire {
	    inputs?: PortSpecWire[];
	    outputs?: PortSpecWire[];
	
	    static createFrom(source: any = {}) {
	        return new PortSetWire(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.inputs = this.convertValues(source["inputs"], PortSpecWire);
	        this.outputs = this.convertValues(source["outputs"], PortSpecWire);
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
	export class NodeManifestSummary {
	    id: string;
	    kindName?: string;
	    displayName?: string;
	    description?: string;
	    category?: string;
	    extends?: string;
	    archetype?: string;
	    callable: boolean;
	    aliases?: string[];
	    source?: string;
	    hash?: string;
	    version?: string;
	
	    static createFrom(source: any = {}) {
	        return new NodeManifestSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kindName = source["kindName"];
	        this.displayName = source["displayName"];
	        this.description = source["description"];
	        this.category = source["category"];
	        this.extends = source["extends"];
	        this.archetype = source["archetype"];
	        this.callable = source["callable"];
	        this.aliases = source["aliases"];
	        this.source = source["source"];
	        this.hash = source["hash"];
	        this.version = source["version"];
	    }
	}
	export class NodeManifestDetail {
	    summary: NodeManifestSummary;
	    chain: string[];
	    attrs: AttrSpecWire[];
	    ports: PortSetWire;
	    defaults?: Record<string, any>;
	    budgetConsumes?: string[];
	    budget?: string;
	    executor?: string;
	    provenance: AttrProvenance[];
	
	    static createFrom(source: any = {}) {
	        return new NodeManifestDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.summary = this.convertValues(source["summary"], NodeManifestSummary);
	        this.chain = source["chain"];
	        this.attrs = this.convertValues(source["attrs"], AttrSpecWire);
	        this.ports = this.convertValues(source["ports"], PortSetWire);
	        this.defaults = source["defaults"];
	        this.budgetConsumes = source["budgetConsumes"];
	        this.budget = source["budget"];
	        this.executor = source["executor"];
	        this.provenance = this.convertValues(source["provenance"], AttrProvenance);
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
	
	
	
	export class ReloadResult {
	    added: string[];
	    removed: string[];
	    modified: string[];
	    errors?: string[];
	    reloadedAt?: string;
	
	    static createFrom(source: any = {}) {
	        return new ReloadResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.added = source["added"];
	        this.removed = source["removed"];
	        this.modified = source["modified"];
	        this.errors = source["errors"];
	        this.reloadedAt = source["reloadedAt"];
	    }
	}
	export class UserOverrideInfo {
	    path: string;
	    filename: string;
	    id?: string;
	    status: string;
	    error?: string;
	    sizeBytes?: number;
	
	    static createFrom(source: any = {}) {
	        return new UserOverrideInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.filename = source["filename"];
	        this.id = source["id"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.sizeBytes = source["sizeBytes"];
	    }
	}

}

export namespace permissions {
	
	export class Grant {
	    id: string;
	    policy_file?: string;
	    transient: boolean;
	    resource_key?: string;
	    family?: string;
	
	    static createFrom(source: any = {}) {
	        return new Grant(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.policy_file = source["policy_file"];
	        this.transient = source["transient"];
	        this.resource_key = source["resource_key"];
	        this.family = source["family"];
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
	    choices?: string[];
	
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
	        this.choices = source["choices"];
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
	    transport?: string;
	    url?: string;
	    headers_template?: Record<string, string>;
	    post_url?: string;
	    env_keys: EnvKey[];
	    capabilities: Capabilities;
	    docs_url: string;
	    init_timeout_ms: number;
	    ping_period_ms: number;
	    sampling_policy: SamplingPolicy;
	    args_template?: string[];
	    config_options?: ConfigOption[];
	    warning?: string;
	    recommended_policy_template?: string;
	    prompt_on_first_use?: string[];
	    pre_seeding_policy?: string;
	
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
	        this.transport = source["transport"];
	        this.url = source["url"];
	        this.headers_template = source["headers_template"];
	        this.post_url = source["post_url"];
	        this.env_keys = this.convertValues(source["env_keys"], EnvKey);
	        this.capabilities = this.convertValues(source["capabilities"], Capabilities);
	        this.docs_url = source["docs_url"];
	        this.init_timeout_ms = source["init_timeout_ms"];
	        this.ping_period_ms = source["ping_period_ms"];
	        this.sampling_policy = this.convertValues(source["sampling_policy"], SamplingPolicy);
	        this.args_template = source["args_template"];
	        this.config_options = this.convertValues(source["config_options"], ConfigOption);
	        this.warning = source["warning"];
	        this.recommended_policy_template = source["recommended_policy_template"];
	        this.prompt_on_first_use = source["prompt_on_first_use"];
	        this.pre_seeding_policy = source["pre_seeding_policy"];
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
	export class ImportEntry {
	    id: string;
	    original_name: string;
	    status: string;
	    reason?: string;
	    recipe: Recipe;
	    original_json?: number[];
	
	    static createFrom(source: any = {}) {
	        return new ImportEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.original_name = source["original_name"];
	        this.status = source["status"];
	        this.reason = source["reason"];
	        this.recipe = this.convertValues(source["recipe"], Recipe);
	        this.original_json = source["original_json"];
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
	
	
	export class TranslationReport {
	    entries: ImportEntry[];
	    kept_count: number;
	    unsupported_count: number;
	    malformed_count: number;
	    collision_count: number;
	
	    static createFrom(source: any = {}) {
	        return new TranslationReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.entries = this.convertValues(source["entries"], ImportEntry);
	        this.kept_count = source["kept_count"];
	        this.unsupported_count = source["unsupported_count"];
	        this.malformed_count = source["malformed_count"];
	        this.collision_count = source["collision_count"];
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
	export class BashExecResult {
	    stdout: string;
	    stderr: string;
	    exitCode: number;
	    truncated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BashExecResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stdout = source["stdout"];
	        this.stderr = source["stderr"];
	        this.exitCode = source["exitCode"];
	        this.truncated = source["truncated"];
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

export namespace search {
	
	export class Highlight {
	    start: number;
	    end: number;
	
	    static createFrom(source: any = {}) {
	        return new Highlight(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.start = source["start"];
	        this.end = source["end"];
	    }
	}
	export class SearchFilters {
	    projectId?: string;
	    sessionId?: string;
	    roleFilter?: string;
	    limit?: number;
	
	    static createFrom(source: any = {}) {
	        return new SearchFilters(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.sessionId = source["sessionId"];
	        this.roleFilter = source["roleFilter"];
	        this.limit = source["limit"];
	    }
	}
	export class SearchHit {
	    sessionId: string;
	    sessionName: string;
	    messageId: string;
	    role: string;
	    snippet: string;
	    highlights: Highlight[];
	    createdAt: string;
	    projectId?: string;
	
	    static createFrom(source: any = {}) {
	        return new SearchHit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.sessionName = source["sessionName"];
	        this.messageId = source["messageId"];
	        this.role = source["role"];
	        this.snippet = source["snippet"];
	        this.highlights = this.convertValues(source["highlights"], Highlight);
	        this.createdAt = source["createdAt"];
	        this.projectId = source["projectId"];
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

export namespace sessions {
	
	export class DeleteOptions {
	    preserveArtifacts?: boolean;
	    promoteArtifactsToProject?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DeleteOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.preserveArtifacts = source["preserveArtifacts"];
	        this.promoteArtifactsToProject = source["promoteArtifactsToProject"];
	    }
	}
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
	    compactedIntoId?: string;
	    compactedAt?: string;
	    archivedAt?: string;
	
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
	        this.compactedIntoId = source["compactedIntoId"];
	        this.compactedAt = source["compactedAt"];
	        this.archivedAt = source["archivedAt"];
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
	export class ListMessagesResult {
	    messages: Message[];
	    sweptCount: number;
	
	    static createFrom(source: any = {}) {
	        return new ListMessagesResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.messages = this.convertValues(source["messages"], Message);
	        this.sweptCount = source["sweptCount"];
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
	    autoTitled: boolean;
	
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
	        this.autoTitled = source["autoTitled"];
	    }
	}
	export class SessionUsage {
	    promptTokens: number;
	    completionTokens: number;
	    totalTokens: number;
	    costUsd: number;
	    costSource: string;
	    messageCount: number;
	    pricingDataDate: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionUsage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.promptTokens = source["promptTokens"];
	        this.completionTokens = source["completionTokens"];
	        this.totalTokens = source["totalTokens"];
	        this.costUsd = source["costUsd"];
	        this.costSource = source["costSource"];
	        this.messageCount = source["messageCount"];
	        this.pricingDataDate = source["pricingDataDate"];
	    }
	}

}

export namespace settings {
	
	export class ProviderProfileRef {
	    providerId?: string;
	    modelId?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProviderProfileRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providerId = source["providerId"];
	        this.modelId = source["modelId"];
	    }
	}
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
	    autoCaptureCodeBlocksDisabled?: boolean;
	    codeBlockMinLines?: number;
	    codeBlockMinBytes?: number;
	    autoCaptureToolOutputsDisabled?: boolean;
	    webSearchEnabled?: boolean;
	    bashEnabled?: boolean;
	    saveArtifactDisabled?: boolean;
	    maxAgentTurns?: number;
	    compactionAggressiveness?: string;
	    compactionModel?: ProviderProfileRef;
	    compactionArchiveDays?: number;
	    compactionRecentWindow?: number;
	    permissionMode?: string;
	    permissionCacheDangerousOps?: boolean;
	    bashAllowlistMigrated?: boolean;
	    permissionsMigrationToastShown?: boolean;
	    cedarStrictCredentialMode?: boolean;
	    credentialAuditRetentionDays?: number;
	    branchAdvisorEnabled?: boolean;
	    branchAdvisorMinConfidence?: number;
	    branchAdvisorUseLLM?: boolean;
	    branchAutoMode?: boolean;
	    branchReintegrationMaxTokens?: number;
	    branchAdvisorDefaultModel?: ProviderProfileRef;
	
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
	        this.autoCaptureCodeBlocksDisabled = source["autoCaptureCodeBlocksDisabled"];
	        this.codeBlockMinLines = source["codeBlockMinLines"];
	        this.codeBlockMinBytes = source["codeBlockMinBytes"];
	        this.autoCaptureToolOutputsDisabled = source["autoCaptureToolOutputsDisabled"];
	        this.webSearchEnabled = source["webSearchEnabled"];
	        this.bashEnabled = source["bashEnabled"];
	        this.saveArtifactDisabled = source["saveArtifactDisabled"];
	        this.maxAgentTurns = source["maxAgentTurns"];
	        this.compactionAggressiveness = source["compactionAggressiveness"];
	        this.compactionModel = this.convertValues(source["compactionModel"], ProviderProfileRef);
	        this.compactionArchiveDays = source["compactionArchiveDays"];
	        this.compactionRecentWindow = source["compactionRecentWindow"];
	        this.permissionMode = source["permissionMode"];
	        this.permissionCacheDangerousOps = source["permissionCacheDangerousOps"];
	        this.bashAllowlistMigrated = source["bashAllowlistMigrated"];
	        this.permissionsMigrationToastShown = source["permissionsMigrationToastShown"];
	        this.cedarStrictCredentialMode = source["cedarStrictCredentialMode"];
	        this.credentialAuditRetentionDays = source["credentialAuditRetentionDays"];
	        this.branchAdvisorEnabled = source["branchAdvisorEnabled"];
	        this.branchAdvisorMinConfidence = source["branchAdvisorMinConfidence"];
	        this.branchAdvisorUseLLM = source["branchAdvisorUseLLM"];
	        this.branchAutoMode = source["branchAutoMode"];
	        this.branchReintegrationMaxTokens = source["branchReintegrationMaxTokens"];
	        this.branchAdvisorDefaultModel = this.convertValues(source["branchAdvisorDefaultModel"], ProviderProfileRef);
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

export namespace tools {
	
	export class RecipeListing {
	    recipe: recipes.Recipe;
	    enabled: boolean;
	    status: transport.RecipeStatus;
	    keysPresent: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RecipeListing(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.recipe = this.convertValues(source["recipe"], recipes.Recipe);
	        this.enabled = source["enabled"];
	        this.status = this.convertValues(source["status"], transport.RecipeStatus);
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

export namespace transport {
	
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

