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
	export class GameBoostState {
	    enabled: boolean;
	    applied: boolean;
	    pid: number;
	    detail: string;
	    isolated: boolean;
	    isolateDetail: string;
	
	    static createFrom(source: any = {}) {
	        return new GameBoostState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.applied = source["applied"];
	        this.pid = source["pid"];
	        this.detail = source["detail"];
	        this.isolated = source["isolated"];
	        this.isolateDetail = source["isolateDetail"];
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

