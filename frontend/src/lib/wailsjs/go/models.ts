export namespace main {
	
	export class APIConfig {
	    enabled: boolean;
	    port: number;
	    apiKey: string;
	    mcpEnabled: boolean;
	    mcpKey: string;
	
	    static createFrom(source: any = {}) {
	        return new APIConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.port = source["port"];
	        this.apiKey = source["apiKey"];
	        this.mcpEnabled = source["mcpEnabled"];
	        this.mcpKey = source["mcpKey"];
	    }
	}
	export class FolderState {
	    Name: string;
	    Path: string;
	    Indexed: boolean;
	    PathOnly: boolean;
	    HasChildren: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FolderState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Name = source["Name"];
	        this.Path = source["Path"];
	        this.Indexed = source["Indexed"];
	        this.PathOnly = source["PathOnly"];
	        this.HasChildren = source["HasChildren"];
	    }
	}
	export class IndexingStatus {
	    isIndexing: boolean;
	    statusMessage: string;
	    progress: number;
	    searchReady: boolean;
	    enhancedReady: boolean;
	
	    static createFrom(source: any = {}) {
	        return new IndexingStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.isIndexing = source["isIndexing"];
	        this.statusMessage = source["statusMessage"];
	        this.progress = source["progress"];
	        this.searchReady = source["searchReady"];
	        this.enhancedReady = source["enhancedReady"];
	    }
	}

}

export namespace shared {
	
	export class SearchWeights {
	    WPathFTS: number;
	    WPathPrefix: number;
	    WSemanticText: number;
	    WSemanticImg: number;
	    WContentFTS: number;
	    WFilename: number;
	    WRecency: number;
	    RecencyHalf: number;
	    RRFK: number;
	    MinScore: number;
	    RerankTopN: number;
	    WRerankerBlend: number;
	    PathOnlyCap: number;
	    UseNewPipeline: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SearchWeights(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.WPathFTS = source["WPathFTS"];
	        this.WPathPrefix = source["WPathPrefix"];
	        this.WSemanticText = source["WSemanticText"];
	        this.WSemanticImg = source["WSemanticImg"];
	        this.WContentFTS = source["WContentFTS"];
	        this.WFilename = source["WFilename"];
	        this.WRecency = source["WRecency"];
	        this.RecencyHalf = source["RecencyHalf"];
	        this.RRFK = source["RRFK"];
	        this.MinScore = source["MinScore"];
	        this.RerankTopN = source["RerankTopN"];
	        this.WRerankerBlend = source["WRerankerBlend"];
	        this.PathOnlyCap = source["PathOnlyCap"];
	        this.UseNewPipeline = source["UseNewPipeline"];
	    }
	}
	export class OptimizerStatus {
	    feedbackCount: number;
	    lastRunAt: string;
	    lastAction: string;
	    preferenceScore: number;
	    baselineScore: number;
	    ndcg10: number;
	    mrr10: number;
	    recallAtRerank: number;
	    downvotePenalty: number;
	    currentWeights: SearchWeights;
	    defaultWeights: SearchWeights;
	    weightDeltas: Record<string, number>;
	
	    static createFrom(source: any = {}) {
	        return new OptimizerStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.feedbackCount = source["feedbackCount"];
	        this.lastRunAt = source["lastRunAt"];
	        this.lastAction = source["lastAction"];
	        this.preferenceScore = source["preferenceScore"];
	        this.baselineScore = source["baselineScore"];
	        this.ndcg10 = source["ndcg10"];
	        this.mrr10 = source["mrr10"];
	        this.recallAtRerank = source["recallAtRerank"];
	        this.downvotePenalty = source["downvotePenalty"];
	        this.currentWeights = this.convertValues(source["currentWeights"], SearchWeights);
	        this.defaultWeights = this.convertValues(source["defaultWeights"], SearchWeights);
	        this.weightDeltas = source["weightDeltas"];
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
	export class SearchResult {
	    Path: string;
	    Score: number;
	    Size: number;
	    Modified: string;
	
	    static createFrom(source: any = {}) {
	        return new SearchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Path = source["Path"];
	        this.Score = source["Score"];
	        this.Size = source["Size"];
	        this.Modified = source["Modified"];
	    }
	}

}

