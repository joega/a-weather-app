import QtQuick
import Quickshell
import Quickshell.Io
import "../Forecast.js" as Forecast
Item {
    id:root
    visible:false
    property var snapshot:null
    property string error:""
    // One already-running idle heartbeat59.4s + shared request53s + margin.
    // Mirrors scripts.run_weather_app.BRIDGE_REQUEST_GRACE.
    readonly property int operationGraceMs:117000
    // Interrupted teardown8s + two worker stops3.2s + effects53s + margin.
    // Mirrors scripts.run_weather_app.BRIDGE_CLEANUP_GRACE.
    readonly property int shutdownGraceMs:70000
    readonly property int terminateGraceMs:3000
    // ui.bridge.BRIDGE_CLEANUP_FAILED_EXIT: reserved for failed owned teardown.
    readonly property int cleanupFailedExitCode:70
    property string buffer:""
    property int nextId:0
    property int pending:-1
    property string pendingOp:""
    property bool closing:false
    property bool disconnected:false
    property bool shutdownFailed:false
    property bool terminationRequested:false
    readonly property bool available:process.running&&!disconnected&&!closing
    property bool stopQueued:false
    property string queuedUserOp:""
    property var queuedUserPatch:null
    // Read-only snapshot polling must not disable controls or close popups.
    // A pending/queued explicit user action still locks ordinary mutations.
    property bool busy:closing||disconnected||stopQueued||queuedUserOp!==""||(pending>=0&&pendingOp!=="snapshot")
    // Quickshell remaps config URLs to qrc; traversing outside that map returns
    // qs-blackhole. shellDir is its trusted absolute filesystem configuration root.
    readonly property string helperPath:Quickshell.shellDir+"/../bridge.py"
    readonly property bool diagnostic:Quickshell.env("A_WEATHER_APP_QML_DIAGNOSTIC")==="1"
    property string stderrBuffer:""
    signal closed(int exitCode)
    function send(op,patch) {
        if(closing&&op!=="quit")return false;
        if(!process.running||disconnected) { if(op!=="snapshot")error="Weather service is unavailable for this action";return false }
        if(pending>=0) {
            if(op==="snapshot")return false;
            if(op==="stop_effects") {
                stopQueued=true;
                if(queuedUserOp==="start_effects"||queuedUserOp==="start_live_effects") { queuedUserOp="";queuedUserPatch=null;error="Pending effects start cancelled by Stop" }
                return true;
            }
            if(queuedUserOp!=="") { error="A control action is already pending. Try again.";return false }
            queuedUserOp=op;queuedUserPatch=patch?JSON.parse(JSON.stringify(patch)):null;
            return true;
        }
        let id=nextId; nextId=(nextId+1)%2147483647;
        // Set the operation first: no transient busy=true for a snapshot.
        pendingOp=op;pending=id;
        let request={request_id:id,op:op};if(op==="set_controls")request.controls=patch;if(op==="set_notifications")request.notifications=patch;if(op==="set_location")request.location=patch;if(op==="select_output")request.output=patch.output;if(op==="start_effects")request.duration=300;
        process.write(JSON.stringify(request)+"\n");deadline.restart();return true;
    }
    function drainUserAction(completedOp) {
        if(closing)return;
        if(stopQueued) { stopQueued=false;send("stop_effects");return }
        if(queuedUserOp!=="") {
            let op=queuedUserOp,patch=queuedUserPatch;
            send(op,patch);queuedUserOp="";queuedUserPatch=null;
        }
    }
    function requestTermination() {
        if(terminationRequested||!process.running)return;
        terminationRequested=true;quitDeliveryTimer.stop();deadline.stop();
        process.signal(15);closeTimer.restart();
    }
    function fail(message) { shutdownFailed=true;disconnected=true;error=message;buffer="";pending=-1;queuedUserOp="";queuedUserPatch=null;stopQueued=false;deadline.stop();requestTermination() }
    function accept(chunk) {
        if(disconnected)return;
        if(chunk.length>262145-buffer.length) { fail("Weather bridge response exceeded its limit");return }
        buffer+=chunk;
        let newline=buffer.indexOf("\n");
        while(newline>=0) {
            let line=buffer.substring(0,newline);buffer=buffer.substring(newline+1);
            try {
                let r=JSON.parse(line);
                Forecast.boundedTree(r);
                if(!Forecast.object(r)||typeof r.request_id!=="number"||Math.floor(r.request_id)!==r.request_id||r.request_id!==pending||typeof r.ok!=="boolean")throw Error("Invalid response");
                let completedOp=pendingOp;pending=-1;deadline.stop();
                if(r.snapshot) { snapshot=Forecast.snapshot(r.snapshot);if(diagnostic)console.log("Weather bridge snapshot accepted:",snapshot.source.freshness) }
                if(completedOp==="quit"&&!r.ok)shutdownFailed=true;
                if(!closing) {
                    if(r.ok)error="";
                    else error=completedOp==="set_location"?"Location change could not start. Try again.":completedOp==="start_effects"||completedOp==="start_live_effects"?"Desktop effects could not start. Check compatibility in Settings.":completedOp==="stop_effects"?"Desktop effects could not stop":completedOp==="check_effects"||completedOp==="select_output"?"Stop desktop effects before changing setup.":["set_notifications","snooze_notifications","resume_notifications"].indexOf(completedOp)>=0?"Notification settings could not be saved. Reopen the app and try again.":"Weather bridge rejected the request";
                }
                drainUserAction(completedOp);
            } catch(e) { fail("Weather bridge returned invalid data");return }
            newline=buffer.indexOf("\n");
        }
    }
    function shutdown() {
        if(closing)return;closing=true;error="Closing weather app…";stopQueued=false;queuedUserOp="";queuedUserPatch=null;poll.stop();deadline.stop();
        if(!process.running){closed(shutdownFailed||disconnected?1:0);return}
        if(terminationRequested)return;
        if(pending<0&&!disconnected){send("quit");deadline.stop();quitDeliveryTimer.restart()}
        else requestTermination();
    }
    Process {
        id:process
        command:["/usr/bin/python3","-I","-B","-S",root.helperPath,"--state-dir",Quickshell.env("A_WEATHER_APP_STATE_DIR")]
        stdinEnabled:true
        running:true
        onStarted: { if(root.diagnostic)console.log("Weather bridge helper:",root.helperPath);root.send("snapshot") }
        stdout:SplitParser { splitMarker:"";onRead:chunk=>root.accept(chunk) }
        stderr:SplitParser { splitMarker:"";onRead:chunk=>{ if(root.stderrBuffer.length<2048)root.stderrBuffer+=chunk.substring(0,2048-root.stderrBuffer.length) } }
        onExited: function(exitCode,exitStatus) {
            root.disconnected=true;
            if(exitCode!==0||exitStatus!==0)root.shutdownFailed=true; // QProcess::NormalExit is zero.
            closeTimer.stop();quitDeliveryTimer.stop();killTimer.stop();
            if(root.diagnostic)console.log("Weather bridge exit:",exitCode,"path rejected:",root.stderrBuffer.indexOf("can't open file")>=0,"helper rejected:",root.stderrBuffer.indexOf("A Weather App UI bridge failed")>=0);
            let uncommitted=root.queuedUserOp!==""||root.pending>=0;
            root.buffer="";root.pending=-1;root.queuedUserOp="";root.queuedUserPatch=null;root.stopQueued=false;deadline.stop();if(root.closing||exitCode===root.cleanupFailedExitCode)root.closed(root.shutdownFailed?1:0);else root.error=uncommitted?"Weather service stopped before the pending action completed. Close and reopen to reconnect.":"Weather service stopped. Close and reopen to reconnect.";root.stderrBuffer="";
        }
    }
    Timer { id:poll;interval:5000;repeat:true;running:!root.closing;onTriggered:root.send("snapshot") }
    Timer { id:deadline;objectName:"bridgeRequestDeadline";interval:root.operationGraceMs;onTriggered:root.fail("Weather request timed out") }
    Timer { id:quitDeliveryTimer;objectName:"bridgeQuitDeliveryGrace";interval:250;onTriggered:root.requestTermination() }
    Timer { id:closeTimer;objectName:"bridgeCloseDeadline";interval:root.shutdownGraceMs;onTriggered:{root.shutdownFailed=true;process.signal(9);killTimer.restart()} }
    Timer { id:killTimer;objectName:"bridgeKillGrace";interval:root.terminateGraceMs;onTriggered:{process.signal(9);if(root.closing)root.closed(1)} }
    Component.onDestruction: { if(process.running)process.signal(15) }
}
