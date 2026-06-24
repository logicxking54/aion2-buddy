export namespace capture {
	
	export class CatalogEntry {
	    name: string;
	    ids: number[];
	
	    static createFrom(source: any = {}) {
	        return new CatalogEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.ids = source["ids"];
	    }
	}
	export class SkillSpeed {
	    name: string;
	    ids: number[];
	    primaryIds: number[];
	    speedPct: number;
	    break: boolean;
	    override: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SkillSpeed(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.ids = source["ids"];
	        this.primaryIds = source["primaryIds"];
	        this.speedPct = source["speedPct"];
	        this.break = source["break"];
	        this.override = source["override"];
	    }
	}

}

export namespace gamemod {
	
	export class Status {
	    moviesDir: string;
	    found: boolean;
	    removed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.moviesDir = source["moviesDir"];
	        this.found = source["found"];
	        this.removed = source["removed"];
	    }
	}
	export class TweakStatus {
	    applied: boolean;
	    detail: string;
	
	    static createFrom(source: any = {}) {
	        return new TweakStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.applied = source["applied"];
	        this.detail = source["detail"];
	    }
	}

}

export namespace main {
	
	export class BlockResult {
	    base: string;
	    rows: memread.MemRow[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new BlockResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.base = source["base"];
	        this.rows = this.convertValues(source["rows"], memread.MemRow);
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
	export class ScanResult {
	    count: number;
	    truncated?: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ScanResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.count = source["count"];
	        this.truncated = source["truncated"];
	        this.error = source["error"];
	    }
	}
	export class SpecResult {
	    ok: boolean;
	    error?: string;
	    module: string;
	    rootOffset: string;
	    offsets: string;
	    address: number;
	    int32: number;
	    uint32: number;
	    int64: number;
	    float32: number;
	    float64: number;
	    hexLE: string;
	
	    static createFrom(source: any = {}) {
	        return new SpecResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.error = source["error"];
	        this.module = source["module"];
	        this.rootOffset = source["rootOffset"];
	        this.offsets = source["offsets"];
	        this.address = source["address"];
	        this.int32 = source["int32"];
	        this.uint32 = source["uint32"];
	        this.int64 = source["int64"];
	        this.float32 = source["float32"];
	        this.float64 = source["float64"];
	        this.hexLE = source["hexLE"];
	    }
	}

}

export namespace memread {
	
	export class MemRow {
	    offset: number;
	    address: number;
	    hex: string;
	    int32: number;
	    uint32: number;
	    float32: number;
	
	    static createFrom(source: any = {}) {
	        return new MemRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.offset = source["offset"];
	        this.address = source["address"];
	        this.hex = source["hex"];
	        this.int32 = source["int32"];
	        this.uint32 = source["uint32"];
	        this.float32 = source["float32"];
	    }
	}
	export class ProcInfo {
	    name: string;
	    pid: number;
	
	    static createFrom(source: any = {}) {
	        return new ProcInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.pid = source["pid"];
	    }
	}
	export class ScanHit {
	    address: number;
	    value: number;
	    display: string;
	
	    static createFrom(source: any = {}) {
	        return new ScanHit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.value = source["value"];
	        this.display = source["display"];
	    }
	}
	export class Status {
	    running: boolean;
	    attached: boolean;
	    pid: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.attached = source["attached"];
	        this.pid = source["pid"];
	        this.error = source["error"];
	    }
	}

}

