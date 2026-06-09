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

export namespace agents {
	
	export class ProfileSummaryWire {
	    id: string;
	    name: string;
	    description: string;
	    model?: string;
	    mergePolicy: string;
	    bundled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProfileSummaryWire(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.model = source["model"];
	        this.mergePolicy = source["mergePolicy"];
	        this.bundled = source["bundled"];
	    }
	}
	export class ProfileWire {
	    id: string;
	    name: string;
	    description: string;
	    whenToUse?: string;
	    model?: string;
	    autonomyTier: string;
	    allowedTools?: string[];
	    deniedTools?: string[];
	    budgetTokens?: number;
	    budgetTimeS?: number;
	    systemPromptOverride?: string;
	    mergePolicy: string;
	    bundled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProfileWire(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.whenToUse = source["whenToUse"];
	        this.model = source["model"];
	        this.autonomyTier = source["autonomyTier"];
	        this.allowedTools = source["allowedTools"];
	        this.deniedTools = source["deniedTools"];
	        this.budgetTokens = source["budgetTokens"];
	        this.budgetTimeS = source["budgetTimeS"];
	        this.systemPromptOverride = source["systemPromptOverride"];
	        this.mergePolicy = source["mergePolicy"];
	        this.bundled = source["bundled"];
	    }
	}

}

export namespace artifacts {
	
	export class ArtifactSourceRef {
	    messageId?: string;
	    toolCallId?: string;
	    codeBlockIndex?: number;
	    filename?: string;
	    absolutePath?: string;
	
	    static createFrom(source: any = {}) {
	        return new ArtifactSourceRef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.messageId = source["messageId"];
	        this.toolCallId = source["toolCallId"];
	        this.codeBlockIndex = source["codeBlockIndex"];
	        this.filename = source["filename"];
	        this.absolutePath = source["absolutePath"];
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

export namespace askuserquestion {
	
	export class PreviewSpec {
	    kind: string;
	    content: string;
	    language?: string;
	
	    static createFrom(source: any = {}) {
	        return new PreviewSpec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.content = source["content"];
	        this.language = source["language"];
	    }
	}
	export class QuestionOption {
	    value: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new QuestionOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.value = source["value"];
	        this.label = source["label"];
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
	export class VerifyChainResult {
	    verified: boolean;
	    rows_checked: number;
	    broken_at_id?: string;
	
	    static createFrom(source: any = {}) {
	        return new VerifyChainResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.verified = source["verified"];
	        this.rows_checked = source["rows_checked"];
	        this.broken_at_id = source["broken_at_id"];
	    }
	}

}

export namespace autonomy {
	
	export class Layer {
	    Level?: number;
	    Overrides: Record<string, any>;
	    PostureMode?: string;
	    PrePlanLayer: number[];
	
	    static createFrom(source: any = {}) {
	        return new Layer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Level = source["Level"];
	        this.Overrides = source["Overrides"];
	        this.PostureMode = source["PostureMode"];
	        this.PrePlanLayer = source["PrePlanLayer"];
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
	    parentMessageId?: string;
	    creationPath?: string;
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
	        this.parentMessageId = source["parentMessageId"];
	        this.creationPath = source["creationPath"];
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
	export class SessionWithBranchPointer {
	    sessionId: string;
	    sessionName: string;
	    createdAt: string;
	    parentSessionId?: string;
	    parentMessageId?: string;
	    branchTitle?: string;
	    branchDepth?: number;
	    parentSessionTitle?: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionWithBranchPointer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.sessionName = source["sessionName"];
	        this.createdAt = source["createdAt"];
	        this.parentSessionId = source["parentSessionId"];
	        this.parentMessageId = source["parentMessageId"];
	        this.branchTitle = source["branchTitle"];
	        this.branchDepth = source["branchDepth"];
	        this.parentSessionTitle = source["parentSessionTitle"];
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
	export class InstallRequest {
	    kind: string;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new InstallRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.path = source["path"];
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

export namespace cedarpolicy {
	
	export class ParseError {
	    line: number;
	    column: number;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ParseError(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.line = source["line"];
	        this.column = source["column"];
	        this.message = source["message"];
	    }
	}
	export class ParseResult {
	    ok: boolean;
	    errors?: ParseError[];
	
	    static createFrom(source: any = {}) {
	        return new ParseResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.errors = this.convertValues(source["errors"], ParseError);
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
	export class PolicyFileDetail {
	    name: string;
	    path: string;
	    bytes: number;
	    embedded: boolean;
	    parse_ok: boolean;
	    parse_err?: string;
	    source: string;
	    read_only: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PolicyFileDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.bytes = source["bytes"];
	        this.embedded = source["embedded"];
	        this.parse_ok = source["parse_ok"];
	        this.parse_err = source["parse_err"];
	        this.source = source["source"];
	        this.read_only = source["read_only"];
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

	export class ContextPublishRequest {
	    node_id: string;
	    layer: string;
	    kind: string;
	    title: string;
	    body: string;
	    team_id?: string;
	    version: number;

	    static createFrom(source: any = {}) {
	        return new ContextPublishRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.node_id = source["node_id"];
	        this.layer = source["layer"];
	        this.kind = source["kind"];
	        this.title = source["title"];
	        this.body = source["body"];
	        this.team_id = source["team_id"];
	        this.version = source["version"];
	    }
	}

	export class ContextPublishResult {
	    accepted_nodes: number;
	    accepted_edges: number;
	    conflicts: fleet.ContextPushConflict[];

	    static createFrom(source: any = {}) {
	        return new ContextPublishResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.accepted_nodes = source["accepted_nodes"];
	        this.accepted_edges = source["accepted_edges"];
	        this.conflicts = this.convertValues(source["conflicts"], fleet.ContextPushConflict);
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

	export class ContextPromoteResult {
	    updated_node_id: string;
	    new_classification: string;

	    static createFrom(source: any = {}) {
	        return new ContextPromoteResult(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.updated_node_id = source["updated_node_id"];
	        this.new_classification = source["new_classification"];
	    }
	}

	export class ContextSyncStatusView {
	    cursor: string;
	    // Go type: time
	    last_pull_at?: any;
	    last_pull_err: string;
	    last_push_err: string;
	    pull_count: number;
	    team_cap_enabled: boolean;

	    static createFrom(source: any = {}) {
	        return new ContextSyncStatusView(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.cursor = source["cursor"];
	        this.last_pull_at = this.convertValues(source["last_pull_at"], null);
	        this.last_pull_err = source["last_pull_err"];
	        this.last_push_err = source["last_push_err"];
	        this.pull_count = source["pull_count"];
	        this.team_cap_enabled = source["team_cap_enabled"];
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

export namespace elicit {
	
	export class DeferredResult {
	    deferred: boolean;
	    ask_id: string;
	
	    static createFrom(source: any = {}) {
	        return new DeferredResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.deferred = source["deferred"];
	        this.ask_id = source["ask_id"];
	    }
	}
	export class WizardDependsOn {
	    question_id: string;
	    condition: number[];
	
	    static createFrom(source: any = {}) {
	        return new WizardDependsOn(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.question_id = source["question_id"];
	        this.condition = source["condition"];
	    }
	}
	export class WizardQuestion {
	    id: string;
	    question: string;
	    kind: string;
	    options?: askuserquestion.QuestionOption[];
	    placeholder?: string;
	    min?: number;
	    max?: number;
	    step?: number;
	    depends_on?: WizardDependsOn;
	
	    static createFrom(source: any = {}) {
	        return new WizardQuestion(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.question = source["question"];
	        this.kind = source["kind"];
	        this.options = this.convertValues(source["options"], askuserquestion.QuestionOption);
	        this.placeholder = source["placeholder"];
	        this.min = source["min"];
	        this.max = source["max"];
	        this.step = source["step"];
	        this.depends_on = this.convertValues(source["depends_on"], WizardDependsOn);
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
	export class ElicitRequest {
	    request_id: string;
	    question: string;
	    kind: string;
	    options?: askuserquestion.QuestionOption[];
	    placeholder?: string;
	    min?: number;
	    max?: number;
	    step?: number;
	    default_value?: number[];
	    preview?: askuserquestion.PreviewSpec;
	    questions?: WizardQuestion[];
	    mode?: string;
	
	    static createFrom(source: any = {}) {
	        return new ElicitRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.request_id = source["request_id"];
	        this.question = source["question"];
	        this.kind = source["kind"];
	        this.options = this.convertValues(source["options"], askuserquestion.QuestionOption);
	        this.placeholder = source["placeholder"];
	        this.min = source["min"];
	        this.max = source["max"];
	        this.step = source["step"];
	        this.default_value = source["default_value"];
	        this.preview = this.convertValues(source["preview"], askuserquestion.PreviewSpec);
	        this.questions = this.convertValues(source["questions"], WizardQuestion);
	        this.mode = source["mode"];
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

export namespace fleet {

	export class ContextPushConflict {
	    node_id: string;
	    server_version: number;
	    client_version: number;

	    static createFrom(source: any = {}) {
	        return new ContextPushConflict(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.node_id = source["node_id"];
	        this.server_version = source["server_version"];
	        this.client_version = source["client_version"];
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
	export class MergedOutput {
	    blocked: boolean;
	    blockReason?: string;
	    additionalContext?: string;
	    updatedInput?: number[];
	    updatedMCPOutput?: number[];
	    permissionDenied: boolean;
	    permissionAllowed: boolean;
	    permissionReason?: string;
	    watchPaths?: string[];
	
	    static createFrom(source: any = {}) {
	        return new MergedOutput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.blocked = source["blocked"];
	        this.blockReason = source["blockReason"];
	        this.additionalContext = source["additionalContext"];
	        this.updatedInput = source["updatedInput"];
	        this.updatedMCPOutput = source["updatedMCPOutput"];
	        this.permissionDenied = source["permissionDenied"];
	        this.permissionAllowed = source["permissionAllowed"];
	        this.permissionReason = source["permissionReason"];
	        this.watchPaths = source["watchPaths"];
	    }
	}
	export class HookOutput {
	    decision?: string;
	    reason?: string;
	    additionalContext?: string;
	    updatedInput?: number[];
	    updatedMCPOutput?: number[];
	    permissionDecision?: string;
	    permissionDecisionReason?: string;
	    watchPaths?: string[];
	    async?: boolean;
	    asyncTimeoutMs?: number;
	
	    static createFrom(source: any = {}) {
	        return new HookOutput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.decision = source["decision"];
	        this.reason = source["reason"];
	        this.additionalContext = source["additionalContext"];
	        this.updatedInput = source["updatedInput"];
	        this.updatedMCPOutput = source["updatedMCPOutput"];
	        this.permissionDecision = source["permissionDecision"];
	        this.permissionDecisionReason = source["permissionDecisionReason"];
	        this.watchPaths = source["watchPaths"];
	        this.async = source["async"];
	        this.asyncTimeoutMs = source["asyncTimeoutMs"];
	    }
	}
	export class DryRunResult {
	    output: HookOutput;
	    merged: MergedOutput;
	    stdout?: string;
	    stderr?: string;
	    exitCode: number;
	    latencyMs: number;
	
	    static createFrom(source: any = {}) {
	        return new DryRunResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.output = this.convertValues(source["output"], HookOutput);
	        this.merged = this.convertValues(source["merged"], MergedOutput);
	        this.stdout = source["stdout"];
	        this.stderr = source["stderr"];
	        this.exitCode = source["exitCode"];
	        this.latencyMs = source["latencyMs"];
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
	    timeoutMs?: number;
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
	        this.timeoutMs = source["timeoutMs"];
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
	export class AttachmentLimitsView {
	    imageInput: boolean;
	    documentInput: boolean;
	    maxImageBytes?: number;
	    maxDocumentBytes?: number;
	    maxImageCountPerMessage?: number;
	    maxImagePixels?: number;
	    maxDocumentPages?: number;
	    imageInputMimeTypes?: string[];
	    documentInputMimeTypes?: string[];
	
	    static createFrom(source: any = {}) {
	        return new AttachmentLimitsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.imageInput = source["imageInput"];
	        this.documentInput = source["documentInput"];
	        this.maxImageBytes = source["maxImageBytes"];
	        this.maxDocumentBytes = source["maxDocumentBytes"];
	        this.maxImageCountPerMessage = source["maxImageCountPerMessage"];
	        this.maxImagePixels = source["maxImagePixels"];
	        this.maxDocumentPages = source["maxDocumentPages"];
	        this.imageInputMimeTypes = source["imageInputMimeTypes"];
	        this.documentInputMimeTypes = source["documentInputMimeTypes"];
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
	export class ImageDimensions {
	    width: number;
	    height: number;
	
	    static createFrom(source: any = {}) {
	        return new ImageDimensions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.width = source["width"];
	        this.height = source["height"];
	    }
	}
	export class MediaSource {
	    kind: string;
	    media_type: string;
	    data?: string;
	    uri?: string;
	    original_name?: string;
	    size_bytes?: number;
	    image_dimensions?: ImageDimensions;
	
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
	        this.size_bytes = source["size_bytes"];
	        this.image_dimensions = this.convertValues(source["image_dimensions"], ImageDimensions);
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
	export class ContentBlock {
	    type: string;
	    text?: string;
	    source?: MediaSource;
	    tool_use?: ToolUse;
	    tool_result?: ToolResult;
	    tool_data?: number[];
	    artifact_id?: string;
	
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
	        this.artifact_id = source["artifact_id"];
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
	
	export class CustomCapabilityMatrix {
	    endpoint: string;
	    probed_at: number;
	    streaming: string;
	    tool_calling: string;
	    streaming_usage: string;
	
	    static createFrom(source: any = {}) {
	        return new CustomCapabilityMatrix(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.endpoint = source["endpoint"];
	        this.probed_at = source["probed_at"];
	        this.streaming = source["streaming"];
	        this.tool_calling = source["tool_calling"];
	        this.streaming_usage = source["streaming_usage"];
	    }
	}
	export class CustomTemplateSummary {
	    id: string;
	    name: string;
	    base_url: string;
	    auth_scheme: string;
	
	    static createFrom(source: any = {}) {
	        return new CustomTemplateSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.base_url = source["base_url"];
	        this.auth_scheme = source["auth_scheme"];
	    }
	}
	export class FallbackChainEntryView {
	    provider_id: string;
	    model?: string;
	    param_overrides?: Record<string, any>;
	    triggers: string[];
	    max_attempts?: number;
	
	    static createFrom(source: any = {}) {
	        return new FallbackChainEntryView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider_id = source["provider_id"];
	        this.model = source["model"];
	        this.param_overrides = source["param_overrides"];
	        this.triggers = source["triggers"];
	        this.max_attempts = source["max_attempts"];
	    }
	}
	export class FallbackChainSummary {
	    id: string;
	    name: string;
	    description?: string;
	    entry_count: number;
	    bundled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FallbackChainSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.entry_count = source["entry_count"];
	        this.bundled = source["bundled"];
	    }
	}
	export class FallbackChainView {
	    id: string;
	    name: string;
	    description?: string;
	    entries: FallbackChainEntryView[];
	
	    static createFrom(source: any = {}) {
	        return new FallbackChainView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.entries = this.convertValues(source["entries"], FallbackChainEntryView);
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
	
	export class LocalRuntimeModel {
	    id: string;
	    displayName: string;
	    sizeBytes?: number;
	    quantLevel?: string;
	    paramCount?: number;
	
	    static createFrom(source: any = {}) {
	        return new LocalRuntimeModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.sizeBytes = source["sizeBytes"];
	        this.quantLevel = source["quantLevel"];
	        this.paramCount = source["paramCount"];
	    }
	}
	export class LocalRuntimeConfigResult {
	    providerId: string;
	    name: string;
	    models: LocalRuntimeModel[];
	
	    static createFrom(source: any = {}) {
	        return new LocalRuntimeConfigResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providerId = source["providerId"];
	        this.name = source["name"];
	        this.models = this.convertValues(source["models"], LocalRuntimeModel);
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
	export class LocalRuntimeInfo {
	    kind: string;
	    name: string;
	    running: boolean;
	    installed: boolean;
	    defaultBaseURL: string;
	    port: number;
	    models?: LocalRuntimeModel[];
	
	    static createFrom(source: any = {}) {
	        return new LocalRuntimeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.name = source["name"];
	        this.running = source["running"];
	        this.installed = source["installed"];
	        this.defaultBaseURL = source["defaultBaseURL"];
	        this.port = source["port"];
	        this.models = this.convertValues(source["models"], LocalRuntimeModel);
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
	    contextWindow?: number;
	    maxOutputTokens?: number;
	
	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.description = source["description"];
	        this.contextWindow = source["contextWindow"];
	        this.maxOutputTokens = source["maxOutputTokens"];
	    }
	}
	export class ProbeCustomEndpointInput {
	    base_url: string;
	    model?: string;
	    auth_scheme: string;
	    auth_header?: string;
	    plaintextApiKey?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProbeCustomEndpointInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.base_url = source["base_url"];
	        this.model = source["model"];
	        this.auth_scheme = source["auth_scheme"];
	        this.auth_header = source["auth_header"];
	        this.plaintextApiKey = source["plaintextApiKey"];
	    }
	}
	export class ProbeCustomEndpointResult {
	    matrix: CustomCapabilityMatrix;
	    err_message?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProbeCustomEndpointResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.matrix = this.convertValues(source["matrix"], CustomCapabilityMatrix);
	        this.err_message = source["err_message"];
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
	    modelInfos?: ModelInfo[];
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
	        this.modelInfos = this.convertValues(source["modelInfos"], ModelInfo);
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
	export class ProviderKeyTestResult {
	    ok: boolean;
	    model_count: number;
	    deprecation_warning?: string;
	    message?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProviderKeyTestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.model_count = source["model_count"];
	        this.deprecation_warning = source["deprecation_warning"];
	        this.message = source["message"];
	    }
	}
	export class RecognizeTemplateResult {
	    matched: boolean;
	    template?: CustomTemplateSummary;
	
	    static createFrom(source: any = {}) {
	        return new RecognizeTemplateResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.matched = source["matched"];
	        this.template = this.convertValues(source["template"], CustomTemplateSummary);
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
	
	export class RotationResult {
	    success: boolean;
	    message?: string;
	    latency_ms: number;
	    // Go type: time
	    tested_at: any;
	    auto_resume_token?: string;
	
	    static createFrom(source: any = {}) {
	        return new RotationResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.latency_ms = source["latency_ms"];
	        this.tested_at = this.convertValues(source["tested_at"], null);
	        this.auto_resume_token = source["auto_resume_token"];
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

export namespace log {
	
	export class FilterQuery {
	    // Go type: time
	    since?: any;
	    // Go type: time
	    until?: any;
	    actor_ids?: string[];
	    kinds?: string[];
	    resources?: string[];
	    outcomes?: string[];
	    free_text?: string;
	    verbose?: boolean;
	    limit?: number;
	    offset?: number;
	
	    static createFrom(source: any = {}) {
	        return new FilterQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.since = this.convertValues(source["since"], null);
	        this.until = this.convertValues(source["until"], null);
	        this.actor_ids = source["actor_ids"];
	        this.kinds = source["kinds"];
	        this.resources = source["resources"];
	        this.outcomes = source["outcomes"];
	        this.free_text = source["free_text"];
	        this.verbose = source["verbose"];
	        this.limit = source["limit"];
	        this.offset = source["offset"];
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
	export class ExportOptions {
	    DataDir: string;
	    Filter: FilterQuery;
	    Format: string;
	    HarnessVersion: string;
	    GitSHA: string;
	    ChainStatus: string;
	
	    static createFrom(source: any = {}) {
	        return new ExportOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.DataDir = source["DataDir"];
	        this.Filter = this.convertValues(source["Filter"], FilterQuery);
	        this.Format = source["Format"];
	        this.HarnessVersion = source["HarnessVersion"];
	        this.GitSHA = source["GitSHA"];
	        this.ChainStatus = source["ChainStatus"];
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
	
	export class SavedQuery {
	    id: string;
	    name: string;
	    query: FilterQuery;
	    // Go type: time
	    created_at: any;
	    user_id?: string;
	
	    static createFrom(source: any = {}) {
	        return new SavedQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.query = this.convertValues(source["query"], FilterQuery);
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.user_id = source["user_id"];
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
	    protocol_version?: string;
	    server_info: transport.Implementation;
	    capabilities: transport.ServerCapabilities;
	    tool_count: number;
	    resource_count: number;
	    prompt_count: number;
	    stderr_tail?: string;
	    duration_ms: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new TestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.protocol_version = source["protocol_version"];
	        this.server_info = this.convertValues(source["server_info"], transport.Implementation);
	        this.capabilities = this.convertValues(source["capabilities"], transport.ServerCapabilities);
	        this.tool_count = source["tool_count"];
	        this.resource_count = source["resource_count"];
	        this.prompt_count = source["prompt_count"];
	        this.stderr_tail = source["stderr_tail"];
	        this.duration_ms = source["duration_ms"];
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

}

export namespace memory {
	
	export class CaptureRateSnapshot {
	    chunksPerMinute: number;
	    embedderHealth: string;
	    // Go type: time
	    lastErrorAt?: any;
	    recentErrorCount: number;
	
	    static createFrom(source: any = {}) {
	        return new CaptureRateSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.chunksPerMinute = source["chunksPerMinute"];
	        this.embedderHealth = source["embedderHealth"];
	        this.lastErrorAt = this.convertValues(source["lastErrorAt"], null);
	        this.recentErrorCount = source["recentErrorCount"];
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
	    kind?: string;
	    retrievalWeight?: number;
	    turnId?: string;
	
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
	        this.kind = source["kind"];
	        this.retrievalWeight = source["retrievalWeight"];
	        this.turnId = source["turnId"];
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
	export class ChunkProvenance {
	    chunkId: string;
	    sourceTurn?: string;
	    hookBoundary?: string;
	    kind?: string;
	    scopePath: string;
	    pinned: boolean;
	    retrievalCount: number;
	    citationCount: number;
	    promotionScore: number;
	    embedderKind?: string;
	    embedDimensions?: number;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new ChunkProvenance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.chunkId = source["chunkId"];
	        this.sourceTurn = source["sourceTurn"];
	        this.hookBoundary = source["hookBoundary"];
	        this.kind = source["kind"];
	        this.scopePath = source["scopePath"];
	        this.pinned = source["pinned"];
	        this.retrievalCount = source["retrievalCount"];
	        this.citationCount = source["citationCount"];
	        this.promotionScore = source["promotionScore"];
	        this.embedderKind = source["embedderKind"];
	        this.embedDimensions = source["embedDimensions"];
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
	export class EmbedderEligibility {
	    hasEligible: boolean;
	    allProfiles: number;
	    eligibleProfiles: number;
	    skippedKinds: string[];
	
	    static createFrom(source: any = {}) {
	        return new EmbedderEligibility(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hasEligible = source["hasEligible"];
	        this.allProfiles = source["allProfiles"];
	        this.eligibleProfiles = source["eligibleProfiles"];
	        this.skippedKinds = source["skippedKinds"];
	    }
	}
	export class HealthActivity {
	    captured: number;
	    pruned: number;
	    promoted: number;
	
	    static createFrom(source: any = {}) {
	        return new HealthActivity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.captured = source["captured"];
	        this.pruned = source["pruned"];
	        this.promoted = source["promoted"];
	    }
	}
	export class HealthCounts {
	    total: number;
	    raw: number;
	    narrative: number;
	    longTermPromoted: number;
	    embedded: number;
	    unembedded: number;
	
	    static createFrom(source: any = {}) {
	        return new HealthCounts(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.raw = source["raw"];
	        this.narrative = source["narrative"];
	        this.longTermPromoted = source["longTermPromoted"];
	        this.embedded = source["embedded"];
	        this.unembedded = source["unembedded"];
	    }
	}
	export class HealthEmbedder {
	    kind: string;
	    model: string;
	    dimensions: number;
	
	    static createFrom(source: any = {}) {
	        return new HealthEmbedder(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.model = source["model"];
	        this.dimensions = source["dimensions"];
	    }
	}
	export class HealthSnapshot {
	    counts: HealthCounts;
	    activity: HealthActivity;
	    embedder: HealthEmbedder;
	    capturedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new HealthSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.counts = this.convertValues(source["counts"], HealthCounts);
	        this.activity = this.convertValues(source["activity"], HealthActivity);
	        this.embedder = this.convertValues(source["embedder"], HealthEmbedder);
	        this.capturedAt = source["capturedAt"];
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
	export class NarrativeJobStatus {
	    id: string;
	    turnId: string;
	    sessionId: string;
	    attempt: number;
	    lastError: string;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new NarrativeJobStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.turnId = source["turnId"];
	        this.sessionId = source["sessionId"];
	        this.attempt = source["attempt"];
	        this.lastError = source["lastError"];
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
	export class NarrativeMetrics {
	    chunkId: string;
	    retrievals: number;
	    citations: number;
	    userPins: number;
	    score: number;
	    // Go type: time
	    lastRetrievedAt?: any;
	    // Go type: time
	    lastCitedAt?: any;
	
	    static createFrom(source: any = {}) {
	        return new NarrativeMetrics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.chunkId = source["chunkId"];
	        this.retrievals = source["retrievals"];
	        this.citations = source["citations"];
	        this.userPins = source["userPins"];
	        this.score = source["score"];
	        this.lastRetrievedAt = this.convertValues(source["lastRetrievedAt"], null);
	        this.lastCitedAt = this.convertValues(source["lastCitedAt"], null);
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
	export class PruneRow {
	    id: string;
	    snippet: string;
	    reason: string;
	    action: string;
	
	    static createFrom(source: any = {}) {
	        return new PruneRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.snippet = source["snippet"];
	        this.reason = source["reason"];
	        this.action = source["action"];
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
	    rows: PruneRow[];
	
	    static createFrom(source: any = {}) {
	        return new PrunePreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.verdicts = this.convertValues(source["verdicts"], PruneVerdict);
	        this.stats = this.convertValues(source["stats"], PruneStats);
	        this.rows = this.convertValues(source["rows"], PruneRow);
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
	
	
	
	export class ScoredChunk {
	    chunk: Chunk;
	    similarity: number;
	    injected: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ScoredChunk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.chunk = this.convertValues(source["chunk"], Chunk);
	        this.similarity = source["similarity"];
	        this.injected = source["injected"];
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
	export class RetrievalReport {
	    sessionId: string;
	    query: string;
	    results: ScoredChunk[];
	    threshold: number;
	    // Go type: time
	    at: any;
	
	    static createFrom(source: any = {}) {
	        return new RetrievalReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.query = source["query"];
	        this.results = this.convertValues(source["results"], ScoredChunk);
	        this.threshold = source["threshold"];
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

export namespace onboarding {
	
	export class Action {
	    id: string;
	    label: string;
	    primary?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Action(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.primary = source["primary"];
	    }
	}
	export class Field {
	    id: string;
	    label: string;
	    placeholder?: string;
	    secret?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Field(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.placeholder = source["placeholder"];
	        this.secret = source["secret"];
	    }
	}
	export class Card {
	    title: string;
	    body?: string;
	    actions?: Action[];
	    fields?: Field[];
	    error_message?: string;
	    provider_hint?: string;
	
	    static createFrom(source: any = {}) {
	        return new Card(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.body = source["body"];
	        this.actions = this.convertValues(source["actions"], Action);
	        this.fields = this.convertValues(source["fields"], Field);
	        this.error_message = source["error_message"];
	        this.provider_hint = source["provider_hint"];
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
	
	export class OnboardingState {
	    firstRun: boolean;
	    completed: boolean;
	    phase?: string;
	    currentState?: string;
	    harnessSelfMCPDisabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new OnboardingState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.firstRun = source["firstRun"];
	        this.completed = source["completed"];
	        this.phase = source["phase"];
	        this.currentState = source["currentState"];
	        this.harnessSelfMCPDisabled = source["harnessSelfMCPDisabled"];
	    }
	}
	export class RestartPhase2Request {
	    starterId: string;
	
	    static createFrom(source: any = {}) {
	        return new RestartPhase2Request(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.starterId = source["starterId"];
	    }
	}
	export class RestartPhase2Response {
	    sessionId: string;
	
	    static createFrom(source: any = {}) {
	        return new RestartPhase2Response(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	    }
	}
	export class StarterSummary {
	    id: string;
	    title: string;
	    description: string;
	    recommendedProvider?: string;
	    recommendedModel?: string;
	    recommendedRecipes?: string[];
	
	    static createFrom(source: any = {}) {
	        return new StarterSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.description = source["description"];
	        this.recommendedProvider = source["recommendedProvider"];
	        this.recommendedModel = source["recommendedModel"];
	        this.recommendedRecipes = source["recommendedRecipes"];
	    }
	}
	export class StepRequest {
	    state: string;
	    event: string;
	    payload?: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new StepRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.event = source["event"];
	        this.payload = source["payload"];
	    }
	}
	export class StepResponse {
	    state: string;
	    card: Card;
	
	    static createFrom(source: any = {}) {
	        return new StepResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.card = this.convertValues(source["card"], Card);
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

export namespace planmode {
	
	export class ApproveRequest {
	    session_id: string;
	    plan_id: string;
	
	    static createFrom(source: any = {}) {
	        return new ApproveRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session_id = source["session_id"];
	        this.plan_id = source["plan_id"];
	    }
	}
	export class ApproveResponse {
	    approved: boolean;
	    session_id: string;
	    plan_id: string;
	
	    static createFrom(source: any = {}) {
	        return new ApproveResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.approved = source["approved"];
	        this.session_id = source["session_id"];
	        this.plan_id = source["plan_id"];
	    }
	}
	export class DiscardRequest {
	    session_id: string;
	    plan_id: string;
	
	    static createFrom(source: any = {}) {
	        return new DiscardRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session_id = source["session_id"];
	        this.plan_id = source["plan_id"];
	    }
	}
	export class DiscardResponse {
	    approved: boolean;
	    reason: string;
	    session_id: string;
	    plan_id: string;
	
	    static createFrom(source: any = {}) {
	        return new DiscardResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.approved = source["approved"];
	        this.reason = source["reason"];
	        this.session_id = source["session_id"];
	        this.plan_id = source["plan_id"];
	    }
	}
	export class EditRequest {
	    session_id: string;
	    plan_id: string;
	    edited_plan: string;
	
	    static createFrom(source: any = {}) {
	        return new EditRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session_id = source["session_id"];
	        this.plan_id = source["plan_id"];
	        this.edited_plan = source["edited_plan"];
	    }
	}
	export class EditResponse {
	    approved: boolean;
	    session_id: string;
	    plan_id: string;
	
	    static createFrom(source: any = {}) {
	        return new EditResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.approved = source["approved"];
	        this.session_id = source["session_id"];
	        this.plan_id = source["plan_id"];
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
	    policyEditorEnabled: boolean;
	    keychainRotationEnabled: boolean;
	    customOpenAIEnabled: boolean;
	    capabilities?: Record<string, boolean>;
	    tier?: string;
	
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
	        this.policyEditorEnabled = source["policyEditorEnabled"];
	        this.keychainRotationEnabled = source["keychainRotationEnabled"];
	        this.customOpenAIEnabled = source["customOpenAIEnabled"];
	        this.capabilities = source["capabilities"];
	        this.tier = source["tier"];
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
	export class ArtifactPreviewConfig {
	    enabled: boolean;
	    maxBytes: number;
	    timeoutMs: number;
	
	    static createFrom(source: any = {}) {
	        return new ArtifactPreviewConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.maxBytes = source["maxBytes"];
	        this.timeoutMs = source["timeoutMs"];
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
	export class EmbedderConfigResult {
	    profileId: string;
	    modelOverride: string;
	
	    static createFrom(source: any = {}) {
	        return new EmbedderConfigResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.modelOverride = source["modelOverride"];
	    }
	}
	export class FeatureFlagInfo {
	    name: string;
	    enabled: boolean;
	    description: string;
	    envVar: string;
	
	    static createFrom(source: any = {}) {
	        return new FeatureFlagInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.enabled = source["enabled"];
	        this.description = source["description"];
	        this.envVar = source["envVar"];
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

export namespace scheduledchat {
	
	export class ChatRunEntry {
	    id: string;
	    name: string;
	    promptTemplate: string;
	    cron: string;
	    timezone?: string;
	    model?: string;
	    outputSink: string;
	    enabled: boolean;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatRunEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.promptTemplate = source["promptTemplate"];
	        this.cron = source["cron"];
	        this.timezone = source["timezone"];
	        this.model = source["model"];
	        this.outputSink = source["outputSink"];
	        this.enabled = source["enabled"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class CreateInput {
	    name: string;
	    promptTemplate: string;
	    cron: string;
	    timezone?: string;
	    model?: string;
	    outputSink?: string;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CreateInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.promptTemplate = source["promptTemplate"];
	        this.cron = source["cron"];
	        this.timezone = source["timezone"];
	        this.model = source["model"];
	        this.outputSink = source["outputSink"];
	        this.enabled = source["enabled"];
	    }
	}
	export class RunSummary {
	    id: string;
	    chatRunId: string;
	    sessionId?: string;
	    status: string;
	    // Go type: time
	    startedAt: any;
	    // Go type: time
	    endedAt?: any;
	    outputSnippet?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new RunSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.chatRunId = source["chatRunId"];
	        this.sessionId = source["sessionId"];
	        this.status = source["status"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.endedAt = this.convertValues(source["endedAt"], null);
	        this.outputSnippet = source["outputSnippet"];
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
	export class UpdateInput {
	    id: string;
	    name: string;
	    promptTemplate: string;
	    cron: string;
	    timezone?: string;
	    model?: string;
	    outputSink?: string;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.promptTemplate = source["promptTemplate"];
	        this.cron = source["cron"];
	        this.timezone = source["timezone"];
	        this.model = source["model"];
	        this.outputSink = source["outputSink"];
	        this.enabled = source["enabled"];
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
	    corpora?: string[];
	
	    static createFrom(source: any = {}) {
	        return new SearchFilters(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projectId = source["projectId"];
	        this.sessionId = source["sessionId"];
	        this.roleFilter = source["roleFilter"];
	        this.limit = source["limit"];
	        this.corpora = source["corpora"];
	    }
	}
	export class SearchHit {
	    corpus?: string;
	    entityId?: string;
	    sessionId: string;
	    sessionName: string;
	    messageId: string;
	    role: string;
	    snippet: string;
	    highlights: Highlight[];
	    createdAt: string;
	    projectId?: string;
	    score?: number;
	
	    static createFrom(source: any = {}) {
	        return new SearchHit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.corpus = source["corpus"];
	        this.entityId = source["entityId"];
	        this.sessionId = source["sessionId"];
	        this.sessionName = source["sessionName"];
	        this.messageId = source["messageId"];
	        this.role = source["role"];
	        this.snippet = source["snippet"];
	        this.highlights = this.convertValues(source["highlights"], Highlight);
	        this.createdAt = source["createdAt"];
	        this.projectId = source["projectId"];
	        this.score = source["score"];
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

export namespace secrets {
	
	export class ExposeRequest {
	    locator: string;
	    description: string;
	    kind: string;
	    plaintext: string;
	
	    static createFrom(source: any = {}) {
	        return new ExposeRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.locator = source["locator"];
	        this.description = source["description"];
	        this.kind = source["kind"];
	        this.plaintext = source["plaintext"];
	    }
	}
	export class SecretRow {
	    ref: string;
	    locator: string;
	    description: string;
	    kind: string;
	    scope: string;
	    // Go type: time
	    exposedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new SecretRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ref = source["ref"];
	        this.locator = source["locator"];
	        this.description = source["description"];
	        this.kind = source["kind"];
	        this.scope = source["scope"];
	        this.exposedAt = this.convertValues(source["exposedAt"], null);
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

export namespace sentry {
	
	export class CachedEntry {
	    id: string;
	    // Go type: time
	    capturedAt: any;
	    kind: string;
	    summary: string;
	    sentryEventId?: string;
	
	    static createFrom(source: any = {}) {
	        return new CachedEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.capturedAt = this.convertValues(source["capturedAt"], null);
	        this.kind = source["kind"];
	        this.summary = source["summary"];
	        this.sentryEventId = source["sentryEventId"];
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
	export class DSNTestResult {
	    ok: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new DSNTestResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.error = source["error"];
	    }
	}
	export class LocalReportResult {
	    path: string;
	    byteCount: number;
	
	    static createFrom(source: any = {}) {
	        return new LocalReportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.byteCount = source["byteCount"];
	    }
	}

}

export namespace sessions {
	
	export class AutonomyKnobValues {
	    maxIterations: number;
	    askOnAmbiguity: string;
	    autoApproveFamilies: string[];
	    tokenCeilingPerTurn: number;
	    recapStyle: string;
	    continueOnError: string;
	    destructiveActionPosture: string;
	    sourceTrace: Record<string, string>;
	    tier: string;
	
	    static createFrom(source: any = {}) {
	        return new AutonomyKnobValues(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.maxIterations = source["maxIterations"];
	        this.askOnAmbiguity = source["askOnAmbiguity"];
	        this.autoApproveFamilies = source["autoApproveFamilies"];
	        this.tokenCeilingPerTurn = source["tokenCeilingPerTurn"];
	        this.recapStyle = source["recapStyle"];
	        this.continueOnError = source["continueOnError"];
	        this.destructiveActionPosture = source["destructiveActionPosture"];
	        this.sourceTrace = source["sourceTrace"];
	        this.tier = source["tier"];
	    }
	}
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
	export class ExportResult {
	    path: string;
	    byteCount: number;
	
	    static createFrom(source: any = {}) {
	        return new ExportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.byteCount = source["byteCount"];
	    }
	}
	export class ToolCall {
	    id: string;
	    name: string;
	    argsSummary: string;
	    latency?: string;
	    usedSecrets?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ToolCall(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.argsSummary = source["argsSummary"];
	        this.latency = source["latency"];
	        this.usedSecrets = source["usedSecrets"];
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
	    streamingFailedAt?: string;
	    streamingFailureKind?: string;
	    streamingRecoverable?: boolean;
	    continuationOf?: string;
	    promptTokens?: number;
	    completionTokens?: number;
	    costUsd?: number;
	    messageCostSource?: string;
	
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
	        this.streamingFailedAt = source["streamingFailedAt"];
	        this.streamingFailureKind = source["streamingFailureKind"];
	        this.streamingRecoverable = source["streamingRecoverable"];
	        this.continuationOf = source["continuationOf"];
	        this.promptTokens = source["promptTokens"];
	        this.completionTokens = source["completionTokens"];
	        this.costUsd = source["costUsd"];
	        this.messageCostSource = source["messageCostSource"];
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
	
	export class ResolvedAutonomy {
	    resolved: AutonomyKnobValues;
	    global: autonomy.Layer;
	    project: autonomy.Layer;
	    session: autonomy.Layer;
	
	    static createFrom(source: any = {}) {
	        return new ResolvedAutonomy(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.resolved = this.convertValues(source["resolved"], AutonomyKnobValues);
	        this.global = this.convertValues(source["global"], autonomy.Layer);
	        this.project = this.convertValues(source["project"], autonomy.Layer);
	        this.session = this.convertValues(source["session"], autonomy.Layer);
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
	    kind?: string;
	    parentSessionId?: string;
	    parentMessageId?: string;
	    branchTitle?: string;
	    branchDepth?: number;
	
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
	        this.kind = source["kind"];
	        this.parentSessionId = source["parentSessionId"];
	        this.parentMessageId = source["parentMessageId"];
	        this.branchTitle = source["branchTitle"];
	        this.branchDepth = source["branchDepth"];
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
	
	export class AuditSettings {
	    strategy?: string;
	    window_days?: number;
	
	    static createFrom(source: any = {}) {
	        return new AuditSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.strategy = source["strategy"];
	        this.window_days = source["window_days"];
	    }
	}
	export class CapabilitiesView {
	    tier: string;
	    enabled: Record<string, boolean>;
	    fetchedAt: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new CapabilitiesView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tier = source["tier"];
	        this.enabled = source["enabled"];
	        this.fetchedAt = source["fetchedAt"];
	        this.source = source["source"];
	    }
	}
	export class FleetConfigPullStatusView {
	    lastAppliedId: number;
	    lastAppliedAt: string;
	    lastError: string;
	    source: string;
	    bundleChecksum: string;
	
	    static createFrom(source: any = {}) {
	        return new FleetConfigPullStatusView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.lastAppliedId = source["lastAppliedId"];
	        this.lastAppliedAt = source["lastAppliedAt"];
	        this.lastError = source["lastError"];
	        this.source = source["source"];
	        this.bundleChecksum = source["bundleChecksum"];
	    }
	}
	export class FleetIdentity {
	    userId: string;
	    orgId: string;
	    teamId: string;
	    email?: string;
	    displayName?: string;
	    tier?: string;
	    orgName?: string;
	    teamName?: string;
	    roles?: string[];
	
	    static createFrom(source: any = {}) {
	        return new FleetIdentity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.userId = source["userId"];
	        this.orgId = source["orgId"];
	        this.teamId = source["teamId"];
	        this.email = source["email"];
	        this.displayName = source["displayName"];
	        this.tier = source["tier"];
	        this.orgName = source["orgName"];
	        this.teamName = source["teamName"];
	        this.roles = source["roles"];
	    }
	}
	export class FleetProfileInfo {
	    name: string;
	    badgeColor: string;
	    fleetBaseUrl: string;
	    configured: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FleetProfileInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.badgeColor = source["badgeColor"];
	        this.fleetBaseUrl = source["fleetBaseUrl"];
	        this.configured = source["configured"];
	    }
	}
	export class LockdownStatusView {
	    active: boolean;
	    reason?: string;
	
	    static createFrom(source: any = {}) {
	        return new LockdownStatusView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.active = source["active"];
	        this.reason = source["reason"];
	    }
	}
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
	    keyboardShortcuts?: Record<string, string>;
	    keyboardShortcutsPreset?: string;
	    fsRequestAccessDisabled?: boolean;
	    searchDisabled?: boolean;
	    autonomy?: number[];
	    autoCheckUpdatesDisabled?: boolean;
	    updateChannel?: string;
	    updateCheckIntervalSec?: number;
	    skippedUpdateVersions?: string[];
	    monthlyCostNotifyUsd?: number;
	    mcpAutoRestartDisabled?: boolean;
	    fsReadDisabled?: boolean;
	    fsWriteDisabled?: boolean;
	    todoDisabled?: boolean;
	    editFileArtifactSyncDisabled?: boolean;
	    contextWindowOverrides?: Record<string, number>;
	    autoCollapseBranchesInSidebar?: boolean;
	    deleteBranchesWithParent?: boolean;
	    maxVisibleBranchDepth?: number;
	    embedderProviderProfileId?: string;
	    embedderModelOverride?: string;
	    showPerMessageTokenMeter?: boolean;
	    longSessionNudgeTurns?: number;
	    longSessionNudgeTokens?: number;
	    memoryNarrativeEnabled?: boolean;
	    summarizerProfileId?: string;
	    narrativePromotionWeights?: string;
	    narrativePromotionThreshold?: number;
	    narrativeRetrievalWeight?: number;
	    narrativePromoterParallelism?: number;
	    narrativePreludeTopN?: number;
	    multimodalInputDisabled?: boolean;
	    autoResumeOnKeyRotationDisabled?: boolean;
	    autoCaptureGeneratedImagesDisabled?: boolean;
	    maxGeneratedImageBytes?: number;
	    localRuntimeRAMOverrideGB?: number;
	    crashReportingTier?: string;
	    sentryDsn?: string;
	    hasSeenCrashReportingOnboarding?: boolean;
	
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
	        this.keyboardShortcuts = source["keyboardShortcuts"];
	        this.keyboardShortcutsPreset = source["keyboardShortcutsPreset"];
	        this.fsRequestAccessDisabled = source["fsRequestAccessDisabled"];
	        this.searchDisabled = source["searchDisabled"];
	        this.autonomy = source["autonomy"];
	        this.autoCheckUpdatesDisabled = source["autoCheckUpdatesDisabled"];
	        this.updateChannel = source["updateChannel"];
	        this.updateCheckIntervalSec = source["updateCheckIntervalSec"];
	        this.skippedUpdateVersions = source["skippedUpdateVersions"];
	        this.monthlyCostNotifyUsd = source["monthlyCostNotifyUsd"];
	        this.mcpAutoRestartDisabled = source["mcpAutoRestartDisabled"];
	        this.fsReadDisabled = source["fsReadDisabled"];
	        this.fsWriteDisabled = source["fsWriteDisabled"];
	        this.todoDisabled = source["todoDisabled"];
	        this.editFileArtifactSyncDisabled = source["editFileArtifactSyncDisabled"];
	        this.contextWindowOverrides = source["contextWindowOverrides"];
	        this.autoCollapseBranchesInSidebar = source["autoCollapseBranchesInSidebar"];
	        this.deleteBranchesWithParent = source["deleteBranchesWithParent"];
	        this.maxVisibleBranchDepth = source["maxVisibleBranchDepth"];
	        this.embedderProviderProfileId = source["embedderProviderProfileId"];
	        this.embedderModelOverride = source["embedderModelOverride"];
	        this.showPerMessageTokenMeter = source["showPerMessageTokenMeter"];
	        this.longSessionNudgeTurns = source["longSessionNudgeTurns"];
	        this.longSessionNudgeTokens = source["longSessionNudgeTokens"];
	        this.memoryNarrativeEnabled = source["memoryNarrativeEnabled"];
	        this.summarizerProfileId = source["summarizerProfileId"];
	        this.narrativePromotionWeights = source["narrativePromotionWeights"];
	        this.narrativePromotionThreshold = source["narrativePromotionThreshold"];
	        this.narrativeRetrievalWeight = source["narrativeRetrievalWeight"];
	        this.narrativePromoterParallelism = source["narrativePromoterParallelism"];
	        this.narrativePreludeTopN = source["narrativePreludeTopN"];
	        this.multimodalInputDisabled = source["multimodalInputDisabled"];
	        this.autoResumeOnKeyRotationDisabled = source["autoResumeOnKeyRotationDisabled"];
	        this.autoCaptureGeneratedImagesDisabled = source["autoCaptureGeneratedImagesDisabled"];
	        this.maxGeneratedImageBytes = source["maxGeneratedImageBytes"];
	        this.localRuntimeRAMOverrideGB = source["localRuntimeRAMOverrideGB"];
	        this.crashReportingTier = source["crashReportingTier"];
	        this.sentryDsn = source["sentryDsn"];
	        this.hasSeenCrashReportingOnboarding = source["hasSeenCrashReportingOnboarding"];
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
	    isUser?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CommandInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.comingSoon = source["comingSoon"];
	        this.isUser = source["isUser"];
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
	export class RunResultWire {
	    kind: string;
	    text: string;
	    renderedArgs?: string[];
	    toolName?: string;
	    metadata?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new RunResultWire(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.text = source["text"];
	        this.renderedArgs = source["renderedArgs"];
	        this.toolName = source["toolName"];
	        this.metadata = source["metadata"];
	    }
	}
	export class UserCommandInput {
	    name: string;
	    kind: string;
	    required: boolean;
	    enumValues?: string[];
	    default?: string;
	    hint?: string;
	
	    static createFrom(source: any = {}) {
	        return new UserCommandInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.required = source["required"];
	        this.enumValues = source["enumValues"];
	        this.default = source["default"];
	        this.hint = source["hint"];
	    }
	}
	export class UserCommandSummaryWire {
	    name: string;
	    scope: string;
	    projectId?: string;
	    kind: string;
	    description: string;
	    modelInvokable: boolean;
	    icon?: string;
	    updatedAt?: number;
	
	    static createFrom(source: any = {}) {
	        return new UserCommandSummaryWire(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.scope = source["scope"];
	        this.projectId = source["projectId"];
	        this.kind = source["kind"];
	        this.description = source["description"];
	        this.modelInvokable = source["modelInvokable"];
	        this.icon = source["icon"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class UserCommandWire {
	    name: string;
	    scope: string;
	    projectId?: string;
	    kind: string;
	    description: string;
	    whenToUse?: string;
	    doesNotHandle?: string;
	    modelInvokable: boolean;
	    icon?: string;
	    hiddenFromPanel?: boolean;
	    body?: string;
	    tool?: string;
	    toolArgsTemplate?: string;
	    inputs?: UserCommandInput[];
	    payloadPath?: string;
	    createdAt?: number;
	    updatedAt?: number;
	
	    static createFrom(source: any = {}) {
	        return new UserCommandWire(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.scope = source["scope"];
	        this.projectId = source["projectId"];
	        this.kind = source["kind"];
	        this.description = source["description"];
	        this.whenToUse = source["whenToUse"];
	        this.doesNotHandle = source["doesNotHandle"];
	        this.modelInvokable = source["modelInvokable"];
	        this.icon = source["icon"];
	        this.hiddenFromPanel = source["hiddenFromPanel"];
	        this.body = source["body"];
	        this.tool = source["tool"];
	        this.toolArgsTemplate = source["toolArgsTemplate"];
	        this.inputs = this.convertValues(source["inputs"], UserCommandInput);
	        this.payloadPath = source["payloadPath"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
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

export namespace storage {
	
	export class DriftEntry {
	    version: number;
	    ledgerId: string;
	    expectedId: string;
	    kind: string;
	    severity: string;
	    suggestion: string;
	
	    static createFrom(source: any = {}) {
	        return new DriftEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.ledgerId = source["ledgerId"];
	        this.expectedId = source["expectedId"];
	        this.kind = source["kind"];
	        this.severity = source["severity"];
	        this.suggestion = source["suggestion"];
	    }
	}
	export class DriftReport {
	    drifts: DriftEntry[];
	
	    static createFrom(source: any = {}) {
	        return new DriftReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.drifts = this.convertValues(source["drifts"], DriftEntry);
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
	
	export class FSAccessResult {
	    granted: boolean;
	    expanded: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new FSAccessResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.granted = source["granted"];
	        this.expanded = source["expanded"];
	        this.message = source["message"];
	    }
	}
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
	
	export class Implementation {
	    name: string;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new Implementation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.version = source["version"];
	    }
	}
	export class PromptsCapability {
	    listChanged?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PromptsCapability(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listChanged = source["listChanged"];
	    }
	}
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
	export class ResourcesCapability {
	    subscribe?: boolean;
	    listChanged?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ResourcesCapability(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.subscribe = source["subscribe"];
	        this.listChanged = source["listChanged"];
	    }
	}
	export class LoggingCapability {
	
	
	    static createFrom(source: any = {}) {
	        return new LoggingCapability(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}
	export class ToolsCapability {
	    listChanged?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ToolsCapability(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listChanged = source["listChanged"];
	    }
	}
	export class ServerCapabilities {
	    tools?: ToolsCapability;
	    resources?: ResourcesCapability;
	    prompts?: PromptsCapability;
	    // Go type: LoggingCapability
	    logging?: any;
	
	    static createFrom(source: any = {}) {
	        return new ServerCapabilities(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tools = this.convertValues(source["tools"], ToolsCapability);
	        this.resources = this.convertValues(source["resources"], ResourcesCapability);
	        this.prompts = this.convertValues(source["prompts"], PromptsCapability);
	        this.logging = this.convertValues(source["logging"], null);
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

export namespace update {
	
	export class StatusOutput {
	    currentVersion: string;
	    available: boolean;
	    availableVersion?: string;
	    channel: string;
	    downloadState: string;
	    downloadProgress?: number;
	    notes?: string;
	    releaseUrl?: string;
	    skippedByUser: boolean;
	    lastCheckedAt?: number;
	
	    static createFrom(source: any = {}) {
	        return new StatusOutput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.currentVersion = source["currentVersion"];
	        this.available = source["available"];
	        this.availableVersion = source["availableVersion"];
	        this.channel = source["channel"];
	        this.downloadState = source["downloadState"];
	        this.downloadProgress = source["downloadProgress"];
	        this.notes = source["notes"];
	        this.releaseUrl = source["releaseUrl"];
	        this.skippedByUser = source["skippedByUser"];
	        this.lastCheckedAt = source["lastCheckedAt"];
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

export namespace workflows {
	
	export class Input {
	    name: string;
	    kind: string;
	    required?: boolean;
	    default?: string;
	    options?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Input(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.required = source["required"];
	        this.default = source["default"];
	        this.options = source["options"];
	    }
	}
	export class StepRun {
	    name: string;
	    kind: string;
	    status: string;
	    output?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new StepRun(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.status = source["status"];
	        this.output = source["output"];
	        this.error = source["error"];
	    }
	}
	export class RunResult {
	    runId: string;
	    workflowId: string;
	    status: string;
	    steps: StepRun[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new RunResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.workflowId = source["workflowId"];
	        this.status = source["status"];
	        this.steps = this.convertValues(source["steps"], StepRun);
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
	export class RunSummary {
	    runId: string;
	    workflowId: string;
	    status: string;
	    // Go type: time
	    startedAt: any;
	    // Go type: time
	    endedAt?: any;
	    error?: string;
	    scheduled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RunSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.workflowId = source["workflowId"];
	        this.status = source["status"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.endedAt = this.convertValues(source["endedAt"], null);
	        this.error = source["error"];
	        this.scheduled = source["scheduled"];
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
	export class Step {
	    name: string;
	    kind: string;
	    userPrompt?: string;
	    cmd?: string;
	    args?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Step(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.userPrompt = source["userPrompt"];
	        this.cmd = source["cmd"];
	        this.args = source["args"];
	    }
	}
	export class Workflow {
	    id: string;
	    name: string;
	    description?: string;
	    version: number;
	    inputs?: Input[];
	    steps: Step[];
	
	    static createFrom(source: any = {}) {
	        return new Workflow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.version = source["version"];
	        this.inputs = this.convertValues(source["inputs"], Input);
	        this.steps = this.convertValues(source["steps"], Step);
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
	export class SaveInput {
	    yaml?: string;
	    workflow?: Workflow;
	
	    static createFrom(source: any = {}) {
	        return new SaveInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.yaml = source["yaml"];
	        this.workflow = this.convertValues(source["workflow"], Workflow);
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
	export class SaveOutput {
	    id: string;
	    name: string;
	    version: number;
	    hash: string;
	    yaml: string;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new SaveOutput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.version = source["version"];
	        this.hash = source["hash"];
	        this.yaml = source["yaml"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class ScheduleEntry {
	    workflowId: string;
	    cron: string;
	    timezone?: string;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ScheduleEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workflowId = source["workflowId"];
	        this.cron = source["cron"];
	        this.timezone = source["timezone"];
	        this.enabled = source["enabled"];
	    }
	}
	export class ScheduleSetInput {
	    workflowId: string;
	    cron: string;
	    timezone?: string;
	
	    static createFrom(source: any = {}) {
	        return new ScheduleSetInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workflowId = source["workflowId"];
	        this.cron = source["cron"];
	        this.timezone = source["timezone"];
	    }
	}
	
	
	export class Summary {
	    id: string;
	    name: string;
	    description?: string;
	    version: number;
	    stepCount: number;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new Summary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.version = source["version"];
	        this.stepCount = source["stepCount"];
	        this.source = source["source"];
	    }
	}

}

