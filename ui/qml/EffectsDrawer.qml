import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Forecast.js" as Forecast
GlassPanel {
    id:root
    // A settings surface must obscure the forecast's text, not layer two
    // readable text grids on top of one another.
    color:"#fa2c455a"
    property var controls:({mode:"live",strength:"normal",manual:{condition:"rain"},fps:30,window_physics:true,accumulation:true,lightning_enabled:false,reduced_motion:false,pause_fullscreen:true})
    property string location:"Location unavailable"
    property var locationSettings:({mode:"default",zip_code:null,busy:false,error:null})
    property string displayedSelection:""
    onLocationSettingsChanged: {
        let selection=locationSettings.mode+":"+(locationSettings.zip_code||"");
        if(selection!==displayedSelection) {
            displayedSelection=selection;
            zipInput.text=locationSettings.mode==="zip"?locationSettings.zip_code:"";
        }
    }
    readonly property bool locationLocked:busy||!serviceAvailable||locationSettings.busy
    function applyZip() { if(!locationLocked&&/^[0-9]{5}$/.test(zipInput.text))locationRequested({mode:"zip",zip_code:zipInput.text}) }
    property string serviceStatus:"Unavailable"
    property string rendererStatus:"Stopped"
    property bool busy:false
    property bool canStart:false
    property bool forecastAvailable:false
    property bool serviceAvailable:true
    property bool effectsRunning:false
    property bool persistent:false
    property var notifications:Forecast.notifications()
    property string timezone:"UTC"
    property var setup:({status:"unchecked",reason:"not_checked",outputs:[],selected_output:null})
    property string actionError:""
    readonly property string compatibilityText: ({
        not_checked:"Check this desktop before turning on effects.",
        ready:"Compatibility checks passed for this desktop.",
        wayland_required:"Desktop effects require a Hyprland Wayland session.",
        session_unavailable:"The current Hyprland session could not be verified. Reopen the app from your desktop and check again.",
        output_unavailable:"The selected monitor is no longer available. Check again to refresh the monitor list.",
        output_selection_required:"Choose which monitor should show desktop effects. Other monitors stay available.",
        unsupported_output:"Desktop effects require a physical, unrotated SDR monitor within the supported size limits. The forecast app remains available.",
        plugin_conflict:"Another native Hyprland plugin is loaded. Effects require an otherwise empty native plugin list; no plugins will be removed by this check.",
        native_missing:"Desktop effects components are missing. Update or reinstall A Weather App, then check again.",
        native_incompatible:"Desktop effects do not match this Hyprland build or the compatibility check failed. Update A Weather App for a matching release. Forecasts remain available.",
        activation_failed:"The desktop changed or effects could not start. Check compatibility again before retrying."
    })[setup.reason]||"Desktop effects unavailable."
    function revealFocus(item) {
        let ancestor=item;
        while(ancestor&&ancestor!==body)ancestor=ancestor.parent;
        if(!ancestor)return;
        let point=item.mapToItem(settingsScroll.contentItem,0,0),next=settingsScroll.contentY;
        if(point.y<next+12)next=point.y-12;
        else if(point.y+item.height>next+settingsScroll.height-12)next=point.y+item.height-settingsScroll.height+12;
        settingsScroll.contentY=Math.max(0,Math.min(next,Math.max(0,settingsScroll.contentHeight-settingsScroll.height)));
    }
    signal closeRequested()
    signal patch(var values)
    signal startRequested()
    signal stopRequested()
    signal locationRequested(var values)
    signal checkRequested()
    signal outputRequested(string output)
    signal notificationsPatch(var values)
    signal notificationPauseRequested()
    signal notificationResumeRequested()
    property string launcherStatus:"ready"
    signal installLauncherRequested()
    signal quitRequested()
    function showSection(section) { settingsScroll.contentY=Math.max(0,Math.min(section.y,Math.max(0,settingsScroll.contentHeight-settingsScroll.height))) }
    ColumnLayout {
        id:settingsHeader
        anchors.top:parent.top;anchors.left:parent.left;anchors.right:parent.right;anchors.margins:28;spacing:14
        RowLayout {
            Layout.fillWidth:true
            Column { Layout.fillWidth:true;spacing:5;PlainLabel{text:"Settings";font.pixelSize:32}PlainLabel{text:"Location, notifications and desktop effects";color:Tokens.secondary;font.pixelSize:14} }
            ActionButton { objectName:"closeEffects";iconName:"close";accessibleLabel:"Close settings";onClicked:root.closeRequested() }
        }
        Flow {
            Layout.fillWidth:true;Layout.preferredHeight:childrenRect.height;spacing:6
            ActionButton { objectName:"settingsApplicationSection";text:"Application";onClicked:root.showSection(applicationSection) }
            ActionButton { objectName:"settingsLocationSection";text:"Location";onClicked:root.showSection(locationSection) }
            ActionButton { objectName:"settingsNotificationsSection";text:"Notifications";onClicked:root.showSection(notificationsSection) }
            ActionButton { objectName:"settingsEffectsSection";text:"Effects";onClicked:root.showSection(effectsSection) }
            ActionButton { objectName:"settingsPreviewSection";text:"Preview";onClicked:root.showSection(previewSection) }
            ActionButton { objectName:"settingsAppearanceSection";text:"Appearance";onClicked:root.showSection(appearanceSection) }
        }
    }
    Flickable {
        id:settingsScroll;objectName:"settingsScroll"
        anchors.top:settingsHeader.bottom;anchors.left:parent.left;anchors.right:parent.right;anchors.bottom:parent.bottom;anchors.margins:28;anchors.topMargin:18
        clip:true;contentHeight:body.height;boundsBehavior:Flickable.StopAtBounds
        ScrollBar.vertical:ScrollBar { }
        ColumnLayout {
            id:body;width:parent.width;spacing:16
            PlainLabel { id:applicationSection;text:"Application launcher";font.pixelSize:23;font.weight:Font.DemiBold }
            PlainLabel { Layout.fillWidth:true;text:"Add A Weather App and its icon to your application menu. This opens the same app without using the bar widget.";wrapMode:Text.Wrap;elide:Text.ElideNone;font.pixelSize:14;color:Tokens.secondary }
            ActionButton { objectName:"installLauncher";text:root.launcherStatus==="installed"?"Launcher installed":"Install application launcher";enabled:root.serviceAvailable&&!root.busy&&root.launcherStatus!=="installed";onClicked:root.installLauncherRequested() }
            PlainLabel { objectName:"launcherStatus";Layout.fillWidth:true;text:({ready:"Installed for your user only. Keep this app in its current location.",installed:"Ready. Reopen your application menu and search for A Weather App.",conflict:"An existing launcher or icon differs. Nothing was overwritten. See the README launcher instructions to resolve it.",unsupported_path:"Move the app to a path without quotes, backslashes, dollar signs, percent signs, equals signs or backticks, then try again.",failed:"Could not install the launcher. Check your user application directory permissions and try again."})[root.launcherStatus];wrapMode:Text.Wrap;elide:Text.ElideNone;font.pixelSize:14;color:Tokens.secondary }
            Rectangle { Layout.fillWidth:true;height:1;color:Tokens.border }
            RowLayout {
                id:locationSection
                Layout.fillWidth:true
                PlainLabel { text:"Location";font.pixelSize:23;font.weight:Font.DemiBold }
            }
            PlainLabel { objectName:"settingsLocation";Layout.fillWidth:true;text:root.location;font.pixelSize:18 }
            PlainLabel { objectName:"locationMode";Layout.fillWidth:true;text:root.locationSettings.mode==="zip"?"ZIP code · "+root.locationSettings.zip_code:root.locationSettings.mode==="auto"?"Local location":root.locationSettings.mode==="custom"?"Custom location":"New York is the fallback. Use your current location or enter a ZIP code.";font.pixelSize:14;color:Tokens.secondary;wrapMode:Text.Wrap;elide:Text.ElideNone }
            PlainLabel { text:"5-digit US ZIP code";font.pixelSize:14;color:Tokens.secondary }
            RowLayout {
                Layout.fillWidth:true
                TextField {
                    id:zipInput;objectName:"locationZip";Layout.fillWidth:true
                    maximumLength:5;validator:RegularExpressionValidator { regularExpression:/[0-9]{5}/ }
                    Accessible.name:"US ZIP code"
                    enabled:!root.locationLocked;inputMethodHints:Qt.ImhDigitsOnly
                    color:Tokens.foreground;font.pixelSize:16;selectByMouse:true
                    background:Rectangle { implicitHeight:42;radius:10;color:"#30435e72";border.color:zipInput.activeFocus?Tokens.accent:Tokens.border }
                    onAccepted:root.applyZip()
                }
                ActionButton { objectName:"applyLocationZip";text:"Apply";primary:true;enabled:!root.locationLocked&&/^[0-9]{5}$/.test(zipInput.text);onClicked:root.applyZip() }
            }
            ActionButton { objectName:"useLocalLocation";text:"Use local location";primary:root.locationSettings.mode==="default";enabled:!root.locationLocked;onClicked:root.locationRequested({mode:"auto"}) }
            PlainLabel { Layout.fillWidth:true;text:"Uses ipwho.is to estimate your city from your public IP. VPNs can affect accuracy.";wrapMode:Text.Wrap;elide:Text.ElideNone;font.pixelSize:13;color:Tokens.secondary }
            PlainLabel {
                objectName:"locationStatus";Layout.fillWidth:true
                visible:root.locationSettings.busy||root.locationSettings.error!==null
                text:root.locationSettings.busy?"Finding location and loading forecast…":({lookup_failed:"Location lookup failed. Try again.",zip_not_found:"ZIP code not found. Check the code and try again.",zip_ambiguous:"This ZIP code matches more than one location. Try another code.",timeout:"Location lookup timed out. Try again.",state_io_failed:"Location could not be saved. Try again.",save_unconfirmed:"Location changed, but saving could not be confirmed. Try again."})[root.locationSettings.error]||""
                wrapMode:Text.Wrap;elide:Text.ElideNone;font.pixelSize:14;color:Tokens.accent
            }
            Rectangle { Layout.fillWidth:true;height:1;color:Tokens.border }
            NotificationsPanel {
                id:notificationsSection;Layout.fillWidth:true
                notifications:root.notifications;timezone:root.timezone;busy:root.busy;serviceAvailable:root.serviceAvailable
                actionError:root.actionError.indexOf("Notification settings")===0?root.actionError:""
                onPatch:values=>root.notificationsPatch(values)
                onPauseRequested:root.notificationPauseRequested()
                onResumeRequested:root.notificationResumeRequested()
                onQuitRequested:root.quitRequested()
            }
            Rectangle { Layout.fillWidth:true;height:1;color:Tokens.border }
            PlainLabel { id:effectsSection;text:"Desktop effects";font.pixelSize:23;font.weight:Font.DemiBold }
            PlainLabel { Layout.fillWidth:true;text:"Optional and off until you start them. The forecast app works without desktop effects.";wrapMode:Text.Wrap;elide:Text.ElideNone;color:Tokens.secondary;font.pixelSize:14 }
            PlainLabel { objectName:"effectsCompatibility";Layout.fillWidth:true;text:root.compatibilityText;wrapMode:Text.Wrap;elide:Text.ElideNone;color:root.setup.status==="ready"?Tokens.accent:Tokens.foreground;font.pixelSize:16 }
            ActionButton { objectName:"checkEffects";text:root.setup.status==="unchecked"?"Check compatibility":"Check again";enabled:root.serviceAvailable&&!root.busy&&!root.effectsRunning;onClicked:root.checkRequested() }
            PlainLabel { Layout.fillWidth:true;text:"Checks the current Hyprland session, monitor and native components without starting effects. Activation checks them again.";wrapMode:Text.Wrap;elide:Text.ElideNone;color:Tokens.secondary;font.pixelSize:13 }
            PlainLabel { visible:root.setup.outputs.length>0;text:"Monitor";font.pixelSize:16 }
            ComboBox {
                id:monitorChoice;objectName:"effectsMonitor";Layout.fillWidth:true
                visible:root.setup.outputs.length>0
                enabled:!root.busy&&!root.effectsRunning&&root.serviceAvailable
                model:root.setup.outputs.map(row=>row.name+" · "+Math.round(row.width)+" × "+Math.round(row.height)+(row.enabled?"":" · Disabled"))
                currentIndex:root.setup.outputs.findIndex(row=>row.name===root.setup.selected_output)
                displayText:currentIndex<0?"Choose a monitor":currentText
                Accessible.name:"Desktop effects monitor"
                onActivated:index=>{if(root.setup.outputs[index].enabled)root.outputRequested(root.setup.outputs[index].name)}
                contentItem:PlainLabel { text:monitorChoice.displayText;verticalAlignment:Text.AlignVCenter;leftPadding:12 }
                background:Rectangle { implicitHeight:42;radius:10;color:"#30435e72";border.color:monitorChoice.activeFocus?Tokens.accent:Tokens.border }
                delegate:ItemDelegate { required property int index;required property string modelData;width:monitorChoice.width;enabled:root.setup.outputs[index].enabled;contentItem:PlainLabel{text:modelData} }
            }
            PlainLabel { Layout.fillWidth:true;text:"Desktop renderer · "+root.rendererStatus;wrapMode:Text.Wrap;elide:Text.ElideNone;color:Tokens.secondary;font.pixelSize:14 }
            RowLayout {
                Layout.fillWidth:true
                ActionButton { objectName:"startPreview";Layout.fillWidth:true;iconName:"play";text:"Start 5-minute preview";primary:true;enabled:root.canStart&&!root.busy&&!root.effectsRunning;onClicked:root.startRequested() }
                ActionButton { objectName:"stopEffects";iconName:"stop";text:"Stop";enabled:root.serviceAvailable&&root.effectsRunning;onClicked:root.stopRequested() }
            }
            PlainLabel { Layout.fillWidth:true;text:!root.serviceAvailable?"Weather service unavailable. Close and reopen to reconnect.":root.persistent?"Live desktop stays on when you close the window. Use Stop or the Live desktop button to turn it off.":!root.forecastAvailable?"Choose a location and load its forecast to enable desktop effects.":"Previews stop after five minutes. Live desktop in the header keeps live weather on until you stop it.";wrapMode:Text.Wrap;elide:Text.ElideNone;color:Tokens.secondary;font.pixelSize:14 }
            PlainLabel { objectName:"effectsActionError";visible:root.actionError!=="";Layout.fillWidth:true;text:root.actionError;wrapMode:Text.Wrap;elide:Text.ElideNone;color:Tokens.accent;font.pixelSize:14 }
            Rectangle { Layout.fillWidth:true;height:1;color:Tokens.border }
            PlainLabel { id:previewSection;text:"Preview";font.pixelSize:23;font.weight:Font.DemiBold }
            PlainLabel { text:"Weather source";font.pixelSize:16 }
            ChoiceControl { Layout.fillWidth:true;testName:"weatherSource";choices:[{label:"Live",value:"live"},{label:"Manual",value:"manual"}];value:root.persistent?"live":root.controls.mode;enabled:!root.busy&&!root.persistent;onChosen:v=>root.patch({mode:v}) }
            PlainLabel { text:"Preview condition";font.pixelSize:16 }
            ComboBox {
                id:conditionChoice;objectName:"previewCondition";Layout.fillWidth:true
                enabled:root.controls.mode==="manual"&&!root.busy&&!root.persistent
                model:["Clear","Partly cloudy","Cloudy","Fog","Drizzle","Rain","Snow","Sleet","Thunderstorm"]
                property var values:["clear","partly_cloudy","cloudy","fog","drizzle","rain","snow","sleet","thunderstorm"]
                currentIndex:Math.max(0,values.indexOf(root.controls.manual.condition))
                Accessible.name:"Preview condition"
                onActivated:index=>root.patch({manual:{condition:values[index]}})
                contentItem:PlainLabel { text:conditionChoice.displayText;verticalAlignment:Text.AlignVCenter;leftPadding:12 }
                background:Rectangle{implicitHeight:42;radius:10;color:"#30435e72";border.color:conditionChoice.activeFocus?Tokens.accent:Tokens.border}
                delegate:ItemDelegate { width:conditionChoice.width;contentItem:PlainLabel{text:modelData} }
            }
            PlainLabel { Layout.fillWidth:true;text:"Manual previews do not change the live forecast. Live desktop always follows the selected location’s weather.";wrapMode:Text.Wrap;elide:Text.ElideNone;font.pixelSize:13;color:Tokens.secondary }
            Rectangle { Layout.fillWidth:true;height:1;color:Tokens.border }
            PlainLabel { id:appearanceSection;text:"Appearance";font.pixelSize:23;font.weight:Font.DemiBold }
            PlainLabel { text:"Intensity";font.pixelSize:16 }
            ChoiceControl { Layout.fillWidth:true;testName:"intensity";choices:[{label:"Subtle",value:"subtle"},{label:"Normal",value:"normal"},{label:"Immersive",value:"immersive"}];value:root.controls.strength;enabled:!root.busy;onChosen:v=>root.patch({strength:v}) }
            PlainLabel { text:"Frame rate";font.pixelSize:16 }
            ChoiceControl { Layout.fillWidth:true;testName:"fps";choices:[{label:"15 FPS",value:15},{label:"30 FPS",value:30},{label:"60 FPS",value:60}];value:root.controls.fps;enabled:!root.busy;onChosen:v=>root.patch({fps:v}) }
            ToggleControl { Layout.fillWidth:true;testName:"windowPhysics";title:"Window physics";caption:"Precipitation collides with window edges.";checked:root.controls.window_physics;locked:root.busy;onToggled:v=>root.patch({window_physics:v}) }
            ToggleControl { Layout.fillWidth:true;testName:"accumulation";title:"Accumulation";caption:"Subtle wetness and buildup on surfaces.";checked:root.controls.accumulation;locked:root.busy;onToggled:v=>root.patch({accumulation:v}) }
            ToggleControl { Layout.fillWidth:true;testName:"lightning";title:"Lightning flashes";caption:"Off by default.";checked:root.controls.lightning_enabled;locked:root.busy;onToggled:v=>root.patch({lightning_enabled:v}) }
            ToggleControl { Layout.fillWidth:true;testName:"reducedMotion";title:"Reduced motion";caption:"Stops precipitation and freezes the sky.";checked:root.controls.reduced_motion;locked:root.busy;onToggled:v=>root.patch({reduced_motion:v}) }
            ToggleControl { Layout.fillWidth:true;testName:"pauseFullscreen";title:"Pause in fullscreen";caption:"Safety pause is always enabled for fullscreen applications.";checked:true;locked:true }
            PlainLabel { Layout.fillWidth:true;text:"Weather service · "+root.serviceStatus;color:Tokens.accent;font.pixelSize:14 }
            PlainLabel { Layout.fillWidth:true;text:"Sky follows the current forecast. Effects use the selected weather source.";wrapMode:Text.Wrap;elide:Text.ElideNone;color:Tokens.secondary;font.pixelSize:14 }
        }
    }
}
