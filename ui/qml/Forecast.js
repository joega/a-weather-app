.pragma library
const conditions = ["clear","partly_cloudy","cloudy","fog","drizzle","rain","snow","sleet","thunderstorm","unknown"];
function object(v) { return v !== null && typeof v === "object" && !Array.isArray(v); }
function string(v,n,multiline) { if(typeof v!=="string" || v.length>n || (multiline ? /[\u0000-\u0008\u000b-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/ : /[\u0000-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/).test(v)) throw Error("Invalid text"); return v; }
function number(v,lo,hi) { if(typeof v!=="number" || !isFinite(v) || v<lo || v>hi) throw Error("Invalid number"); return v; }
function optional(v,lo,hi) { return v===null || v===undefined ? null : number(v,lo,hi); }
function time(v) { string(v,40); if(!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,6})?(Z|[+-]\d{2}:\d{2})$/.test(v)||!isFinite(Date.parse(v))) throw Error("Invalid time"); return v; }
function boundedTree(value) {
    let stack=[{value:value,depth:0}],count=0;
    while(stack.length) {
        let entry=stack.pop();if(++count>8192||entry.depth>16)throw Error("JSON complexity exceeded");
        let v=entry.value;if(v===null||typeof v!=="object")continue;
        let keys=Object.keys(v);if(keys.length>(Array.isArray(v)?240:64))throw Error("JSON cardinality exceeded");
        for(let key of keys) {if(key==="__proto__"||key==="constructor"||key==="prototype")throw Error("Invalid key");stack.push({value:v[key],depth:entry.depth+1})}
    }
}
function condition(v) { if(conditions.indexOf(v)<0) throw Error("Invalid condition"); return v; }
function atmosphere(v) {
    if(!object(v))throw Error("Invalid atmosphere");
    for(let key of ["reduced_motion","lightning_enabled","thunderstorm"])if(typeof v[key]!=="boolean")throw Error("Invalid atmosphere flag");
    return {rain_intensity:number(v.rain_intensity,0,1),snow_intensity:number(v.snow_intensity,0,1),cloud_cover:number(v.cloud_cover,0,1),fog_density:number(v.fog_density,0,1),sun_elevation:number(v.sun_elevation,-90,90),sun_azimuth:number(v.sun_azimuth,0,360),wind_x:number(v.wind_x,-500,500),reduced_motion:v.reduced_motion,lightning_enabled:v.lightning_enabled,thunderstorm:v.thunderstorm};
}
function weather(v) {
    if(!object(v)) throw Error("Invalid weather");
    if(v.is_day!==null&&typeof v.is_day!=="boolean")throw Error("Invalid daylight");
    return {time:time(v.time),condition:condition(v.condition),
        temperature_c:optional(v.temperature_c,-150,100),apparent_temperature_c:optional(v.apparent_temperature_c,-200,150),
        humidity:optional(v.humidity,0,1),cloud_cover:optional(v.cloud_cover,0,1),
        precipitation_rate_mm_hr:optional(v.precipitation_rate_mm_hr,0,10000),precipitation_probability:optional(v.precipitation_probability,0,1),
        visibility_m:optional(v.visibility_m,0,1000000),wind_speed_m_s:optional(v.wind_speed_m_s,0,200),
        wind_gust_m_s:optional(v.wind_gust_m_s,0,250),wind_direction_deg:optional(v.wind_direction_deg,0,360),
        is_day:typeof v.is_day==="boolean"?v.is_day:true};
}
function forecast(v) {
    if(v===null || v===undefined) return null;
    if(!object(v)||!object(v.location)||!object(v.source)||!Array.isArray(v.hourly)||v.hourly.length>240||!Array.isArray(v.daily)||v.daily.length>10) throw Error("Invalid forecast");
    let tz=string(v.location.timezone,80); if(!/^[A-Za-z0-9_+\-/]+$/.test(tz)) throw Error("Invalid timezone");
    let result={location:{name:string(v.location.name,244),timezone:tz},source:{name:string(v.source.name,80),attribution:string(v.source.attribution,240)},fetched_at:time(v.fetched_at),current:weather(v.current),hourly:[],daily:[]};
    for(let i=0;i<v.hourly.length;i++) {
        let row=v.hourly[i],h=weather(row);
        h.local_hour=string(row.local_hour,32);
        h.local_date=row.local_date===undefined?"":string(row.local_date,10);
        if(h.local_date!==""&&!/^\d{4}-\d{2}-\d{2}$/.test(h.local_date))throw Error("Invalid local date");
        h.local_label=row.local_label===undefined?h.local_hour:string(row.local_label,64);
        h.period_label=row.period_label===undefined?"":string(row.period_label,64);
        result.hourly.push(h);
    }
    for(let i=0;i<v.daily.length;i++) { let d=v.daily[i]; if(!object(d)||!/^\d{4}-\d{2}-\d{2}$/.test(d.date)) throw Error("Invalid day"); let lo=optional(d.low_c,-150,100),hi=optional(d.high_c,-150,100);if(lo!==null&&hi!==null&&lo>hi)throw Error("Invalid range");result.daily.push({date:d.date,day_label:string(d.day_label,32),sunrise_label:d.sunrise_label===null?null:string(d.sunrise_label,32),sunset_label:d.sunset_label===null?null:string(d.sunset_label,32),low_c:lo,high_c:hi,condition:condition(d.condition),sunrise:d.sunrise===null?null:time(d.sunrise),sunset:d.sunset===null?null:time(d.sunset),precipitation_probability:optional(d.precipitation_probability,0,1)}); }
    return result;
}
function effectsSetup(v) {
    if(v===undefined)return {status:"unchecked",reason:"not_checked",outputs:[],selected_output:null};
    const reasons=["not_checked","ready","wayland_required","session_unavailable","output_unavailable","unsupported_output","multiple_outputs","plugin_conflict","native_missing","native_incompatible","activation_failed"];
    if(!object(v)||Object.keys(v).length!==4||["unchecked","ready","unavailable"].indexOf(v.status)<0||reasons.indexOf(v.reason)<0||!Array.isArray(v.outputs)||v.outputs.length>32)throw Error("Invalid effects setup");
    let outputs=[],names=[];
    for(let row of v.outputs) {
        if(!object(row)||Object.keys(row).length!==5||typeof row.name!=="string"||!/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/.test(row.name)||names.indexOf(row.name)>=0||typeof row.enabled!=="boolean")throw Error("Invalid output");
        let width=number(row.width,0,32768),height=number(row.height,0,32768);
        if(row.enabled&&(width<=0||height<=0))throw Error("Invalid enabled output");
        outputs.push({name:row.name,width:width,height:height,scale:number(row.scale,.25,8),enabled:row.enabled});names.push(row.name);
    }
    if(v.selected_output!==null&&!outputs.some(row=>row.name===v.selected_output&&row.enabled))throw Error("Invalid output selection");
    if((v.status==="ready")!==(v.reason==="ready")||(v.status==="unchecked")!==(v.reason==="not_checked")||(v.status==="ready"&&(outputs.length!==1||v.selected_output===null)))throw Error("Invalid compatibility state");
    return {status:v.status,reason:v.reason,outputs:outputs,selected_output:v.selected_output};
}
function notifications(v) {
    if(v===undefined)return {settings:{enabled:false,quiet_enabled:true,quiet_start:22,quiet_end:7,probability:50},state:"off",snoozed_until:null,delivery:"none",supported:false};
    if(!object(v)||Object.keys(v).length!==5||!object(v.settings)||Object.keys(v.settings).length!==5||typeof v.supported!=="boolean"||["off","waiting","watching","quiet","paused","unavailable"].indexOf(v.state)<0||["none","pending","sent","failed"].indexOf(v.delivery)<0)throw Error("Invalid notification state");
    let s=v.settings;
    if(typeof s.enabled!=="boolean"||typeof s.quiet_enabled!=="boolean"||[50,70].indexOf(s.probability)<0)throw Error("Invalid notification settings");
    for(let name of ["quiet_start","quiet_end"])if(typeof s[name]!=="number"||Math.floor(s[name])!==s[name]||s[name]<0||s[name]>23)throw Error("Invalid quiet hour");
    if(s.quiet_start===s.quiet_end)throw Error("Invalid quiet period");
    let until=v.snoozed_until===null?null:number(v.snoozed_until,0,253402300799);
    if(v.state==="paused"&&until===null)throw Error("Missing pause expiry");
    return {settings:{enabled:s.enabled,quiet_enabled:s.quiet_enabled,quiet_start:s.quiet_start,quiet_end:s.quiet_end,probability:s.probability},state:v.state,snoozed_until:until,delivery:v.delivery,supported:v.supported};
}
function snapshot(v) {
    if(!object(v)||v.schema_version!==1||!object(v.controls)||!object(v.alerts)||!object(v.source)||!object(v.location)) throw Error("Invalid snapshot");
    if(!Array.isArray(v.alerts.items)||v.alerts.items.length>8||["available","unavailable"].indexOf(v.alerts.status)<0)throw Error("Invalid alerts");
    let controls={};
    for(let k of ["reduced_motion","lightning_enabled","window_physics","accumulation","pause_fullscreen"]) { if(typeof v.controls[k]!=="boolean")throw Error("Invalid control");controls[k]=v.controls[k] }
    if([15,30,60].indexOf(v.controls.fps)<0||["live","manual"].indexOf(v.controls.mode)<0||["subtle","normal","immersive"].indexOf(v.controls.strength)<0||!object(v.controls.manual))throw Error("Invalid controls");
    if(["F","C"].indexOf(v.controls.units)<0)throw Error("Invalid units");
    controls.units=v.controls.units;controls.fps=v.controls.fps;controls.mode=v.controls.mode;controls.strength=v.controls.strength;controls.manual={condition:condition(v.controls.manual.condition)};
    let fresh=v.source.freshness;if(["fresh","stale","expired","invalid_future","unavailable"].indexOf(fresh)<0)throw Error("Invalid freshness");
    let f=null;
    if(v.current!==null) f=forecast({location:v.location,source:v.source,fetched_at:v.source.fetched_at,current:v.current,hourly:v.hourly,daily:v.daily});
    else if(!Array.isArray(v.hourly)||v.hourly.length!==0||!Array.isArray(v.daily)||v.daily.length!==0)throw Error("Invalid empty forecast");
    let alerts=[];
    for(let a of v.alerts.items) {
        if(!object(a))throw Error("Invalid alert");
        if(a.text_truncated!==undefined&&typeof a.text_truncated!=="boolean")throw Error("Invalid alert truncation flag");
        const text=(value,limit,multiline)=>value===null||value===undefined?"":string(value,limit,multiline);
        alerts.push({id:text(a.id,256),event:text(a.event,160)||"Weather alert",headline:text(a.headline,512),description:text(a.description,4096,true),instruction:text(a.instruction,2048,true),severity:text(a.severity,40)||"Unknown",urgency:text(a.urgency,40)||"Unknown",expires:a.expires===undefined?null:time(a.expires),expires_label:text(a.expires_label,64),text_truncated:a.text_truncated===true});
    }
    if(!object(v.effect_status)||["stopped","starting","running","cleanup_failed"].indexOf(v.effect_status.state)<0)throw Error("Invalid effects state");
    const remaining=v.effect_status.remaining_seconds===undefined?0:number(v.effect_status.remaining_seconds,0,300);
    if(v.effect_status.persistent!==undefined&&typeof v.effect_status.persistent!=="boolean")throw Error("Invalid persistent effects state");
    if(typeof v.source.refreshing!=="boolean")throw Error("Invalid refresh status");
    let settings={mode:"default",zip_code:null,busy:false,error:null};
    if(v.location_settings!==undefined) {
        let s=v.location_settings;
        if(!object(s)||Object.keys(s).length!==4||["default","custom","zip","auto"].indexOf(s.mode)<0||typeof s.busy!=="boolean"||(s.zip_code!==null&&(typeof s.zip_code!=="string"||!/^[0-9]{5}$/.test(s.zip_code)))||(s.mode==="zip")!==(s.zip_code!==null)||(s.error!==null&&["lookup_failed","zip_not_found","zip_ambiguous","timeout","state_io_failed","save_unconfirmed"].indexOf(s.error)<0))throw Error("Invalid location settings");
        settings={mode:s.mode,zip_code:s.zip_code,busy:s.busy,error:s.error};
    }
    return {forecast:f,location:string(v.location.name,244),location_settings:settings,effects_setup:effectsSetup(v.effects_setup),notifications:notifications(v.notifications),timezone:string(v.location.timezone,80),controls:controls,atmosphere:atmosphere(v.atmosphere),alerts:{status:v.alerts.status,items:alerts},source:{name:string(v.source.name,80),attribution:string(v.source.attribution,240),freshness:fresh,age_seconds:optional(v.source.age_seconds,0,315360000),refreshing:v.source.refreshing,error:v.source.error===null?null:string(v.source.error,80)},effect_status:v.effect_status.state,effect_remaining_seconds:remaining,effect_persistent:v.effect_status.persistent===true};
}
function temp(v,units) { return v===null||v===undefined ? "—" : Math.round(units==="F" ? v*9/5+32:v)+"°"; }
function title(v) { return ({clear:"Clear",partly_cloudy:"Partly cloudy",cloudy:"Cloudy",fog:"Fog",drizzle:"Drizzle",rain:"Rain",snow:"Snow",sleet:"Sleet",thunderstorm:"Thunderstorm",unknown:"Unavailable"})[v] || "Unavailable"; }
function percent(v) { return v===null||v===undefined?"—":Math.round(v*100)+"%"; }
function localTime(v,tz,kind) {
    if(!v) return "—";
    try { return new Date(v).toLocaleString("en-US",{timeZone:tz,hour:"numeric",minute:kind==="hour"?undefined:"2-digit",hour12:true}); } catch(e) { return "Time unavailable"; }
}
function wind(v,units) { return v===null||v===undefined?"—":Math.round(v*(units==="F"?2.236936:3.6))+(units==="F"?" mph":" km/h"); }
function direction(v) { return v===null||v===undefined?"": ["N","NE","E","SE","S","SW","W","NW"][Math.round(v/45)%8]; }
function amount(v,units) { return v===null||v===undefined?"—":(units==="F"?(v/25.4).toFixed(2)+" in":v.toFixed(1)+" mm"); }
function outlook(hours,units) {
    // Describe the available hourly model, never invent minute-level onset or
    // turn missing probability into a dry-weather promise.
    const next=hours.slice(0,6);
    if(!next.length)return "Hourly outlook unavailable.";
    const known=next.filter(h=>h.precipitation_probability!==null&&h.precipitation_probability!==undefined);
    let parts=[];
    if(known.length===next.length) {
        const peak=known.reduce((a,b)=>a.precipitation_probability>=b.precipitation_probability?a:b);
        parts.push(peak.precipitation_probability<0.2
            ? "Low precipitation chances in the next "+next.length+" hourly forecasts."
            : "Precipitation chance peaks at "+percent(peak.precipitation_probability)+" around "+peak.local_hour+".");
    } else parts.push("Precipitation outlook is incomplete.");
    const gusts=next.filter(h=>h.wind_gust_m_s!==null&&h.wind_gust_m_s!==undefined);
    if(gusts.length===next.length) {
        const peak=Math.max.apply(null,gusts.map(h=>h.wind_gust_m_s));
        if(peak>=8)parts.push("Gusts up to "+wind(peak,units)+".");
    }
    return parts.join(" ");
}
