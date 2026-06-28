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
	
	export class SkillEffectInfo {
	    found: boolean;
	    paksDir: string;
	    applied: boolean;
	    gameVersion: string;
	    supported: string;
	    compatible: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SkillEffectInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.found = source["found"];
	        this.paksDir = source["paksDir"];
	        this.applied = source["applied"];
	        this.gameVersion = source["gameVersion"];
	        this.supported = source["supported"];
	        this.compatible = source["compatible"];
	    }
	}
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

