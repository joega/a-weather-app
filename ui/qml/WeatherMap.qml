import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Rectangle {
    id: root
    objectName: "weatherMap"
    color: "#f3243850"
    radius: 18
    border.color: "#668cb1c7"
    border.width: 1
    property var mapState: ({status:"loading",mapData:null,error:"",offline:false})
    property string location: ""
    property string timezone: "UTC"
    property string units: "F"
    property int hourIndex: 0
    property string mapLayer: "temperature"
    property var tileImages: ({})
    property int tileFailures:0
    signal closeRequested()
    readonly property var mapData: mapState.data
    readonly property var fieldValues: mapData && hourIndex<mapData.hours.length ? mapData.cells.map(c=>mapLayer==="temperature"?c.temperature_c[hourIndex]:mapLayer==="precipitation"?c.precipitation_mm[hourIndex]:c.wind_speed_m_s[hourIndex]) : []
    readonly property real fieldMin: fieldValues.length ? (mapLayer==="precipitation"?0:Math.min.apply(null,fieldValues)) : 0
    readonly property real fieldMax: fieldValues.length ? (mapLayer==="precipitation"?Math.max(0.1,Math.max.apply(null,fieldValues)):Math.max.apply(null,fieldValues)) : 1
    readonly property real centerLat: mapData ? mapData.latitude : 0
    readonly property real centerLon: mapData ? mapData.longitude : 0
    readonly property real desiredScale: Math.min(mapArea.width,mapArea.height)*0.43/16093.44
    readonly property int zoom: Math.max(1,Math.min(15,Math.floor(Math.log(40075016.686*Math.cos(centerLat*Math.PI/180)*desiredScale/256)/Math.log(2))))
    readonly property real tileScale: desiredScale*40075016.686*Math.cos(centerLat*Math.PI/180)/(256*Math.pow(2,zoom))
    readonly property real centerX: worldX(centerLon)
    readonly property real centerY: worldY(centerLat)
    function worldX(lon) {return (lon+180)/360*256*Math.pow(2,zoom)}
    function worldY(lat) {let r=Math.max(-85,Math.min(85,lat))*Math.PI/180;return (1-Math.log(Math.tan(r)+1/Math.cos(r))/Math.PI)/2*256*Math.pow(2,zoom)}
    function mapX(lon) {let dx=worldX(lon)-centerX,n=256*Math.pow(2,zoom);if(dx>n/2)dx-=n;if(dx< -n/2)dx+=n;return mapArea.width/2+dx*tileScale}
    function mapY(lat) {return mapArea.height/2+(worldY(lat)-centerY)*tileScale}
    function tiles() {
        if(!mapData||mapArea.width<1||mapArea.height<1)return [];
        let tileSize=256*tileScale, x0=Math.floor((centerX-mapArea.width/2/tileScale)/256),x1=Math.floor((centerX+mapArea.width/2/tileScale)/256);
        let y0=Math.floor((centerY-mapArea.height/2/tileScale)/256),y1=Math.floor((centerY+mapArea.height/2/tileScale)/256),n=Math.pow(2,zoom),result=[];
        for(let y=y0;y<=y1;y++)for(let x=x0;x<=x1;x++)if(y>=0&&y<n&&result.length<16){let wrapped=(x%n+n)%n;result.push({x:x,y:y,key:zoom+"/"+wrapped+"/"+y,requestX:wrapped,left:mapArea.width/2+(x*256-centerX)*tileScale,top:mapArea.height/2+(y*256-centerY)*tileScale,size:tileSize})}
        return result;
    }
    function windSpeed(value) {return units==="F"?(value*2.236936).toFixed(0)+" mph":value.toFixed(1)+" m/s"}
    function temperature(value) {return units==="F"?(value*1.8+32).toFixed(0)+"°F":value.toFixed(0)+"°C"}
    function rain(value) {return units==="F"?(value/25.4).toFixed(2)+" in":value.toFixed(1)+" mm"}
    function ramp(value,min,max) {let p=Math.max(0,Math.min(1,(value-min)/Math.max(0.01,max-min)));return Qt.rgba(0.13+0.8*p,0.49-0.18*p,0.88-0.68*p,0.47)}
    function shade(value,min,max) {return mapLayer==="precipitation"?(value<=0?Qt.rgba(0,0,0,0):Qt.rgba(0.05,0.36,0.92,0.20+0.55*Math.sqrt(value/Math.max(0.1,max)))):ramp(value,min,max)}
    function paint() {
        let ctx=overlay.getContext("2d");ctx.clearRect(0,0,overlay.width,overlay.height);
        if(!mapData||hourIndex>=mapData.hours.length)return;
        let cells=mapData.cells,vals=cells.map(c=>mapLayer==="temperature"?c.temperature_c[hourIndex]:mapLayer==="precipitation"?c.precipitation_mm[hourIndex]:c.wind_speed_m_s[hourIndex]);
        if(mapLayer!=="wind") {
            if(mapData.resolution_km>=10) {ctx.fillStyle=shade(vals[12],root.fieldMin,root.fieldMax);ctx.fillRect(0,0,overlay.width,overlay.height)}
            else for(let r=0;r<5;r++)for(let c=0;c<5;c++) {
                let i=r*5+c,cell=cells[i],prev=c>0?cells[i-1]:null,next=c<4?cells[i+1]:null,above=r>0?cells[i-5]:null,below=r<4?cells[i+5]:null;
                let x=mapX(cell.longitude),y=mapY(cell.latitude),xl=prev?(x+mapX(prev.longitude))/2:x-25,xr=next?(x+mapX(next.longitude))/2:x+25,yt=above?(y+mapY(above.latitude))/2:y-25,yb=below?(y+mapY(below.latitude))/2:y+25;
                ctx.fillStyle=shade(vals[i],root.fieldMin,root.fieldMax);ctx.fillRect(Math.min(xl,xr),Math.min(yt,yb),Math.abs(xr-xl)+1,Math.abs(yb-yt)+1);
            }
        } else {
            let seen={};for(let i=0;i<cells.length;i++){let cell=cells[i],x=mapX(cell.longitude),y=mapY(cell.latitude),key=Math.round(x/4)+":"+Math.round(y/4);if(seen[key])continue;seen[key]=true;
                let radians=(cell.wind_from_deg[hourIndex]+90)*Math.PI/180,dx=Math.cos(radians)*15,dy=Math.sin(radians)*15;
                ctx.strokeStyle="#11334a";ctx.fillStyle="#11334a";ctx.lineWidth=2.5;ctx.beginPath();ctx.moveTo(x-dx,y-dy);ctx.lineTo(x+dx,y+dy);ctx.stroke();
                ctx.beginPath();ctx.moveTo(x+dx,y+dy);ctx.lineTo(x+dx-Math.cos(radians-0.6)*8,y+dy-Math.sin(radians-0.6)*8);ctx.lineTo(x+dx-Math.cos(radians+0.6)*8,y+dy-Math.sin(radians+0.6)*8);ctx.closePath();ctx.fill();
                ctx.font="bold 12px sans-serif";ctx.fillStyle="#102d42";ctx.fillText(windSpeed(cell.wind_speed_m_s[hourIndex]),x-19,y-19);
            }
        }
    }
    onMapDataChanged: {hourIndex=0;tileImages={};tileFailures=0;Qt.callLater(()=>overlay.requestPaint())}
    onHourIndexChanged:overlay.requestPaint()
    onMapLayerChanged:overlay.requestPaint()
    onWidthChanged:overlay.requestPaint()
    onHeightChanged:overlay.requestPaint()
    Component.onDestruction:mapTiles.close()
    Connections {target:mapTiles;function onTileReady(key,dataURL){let next=Object.assign({},root.tileImages);next[key]=dataURL;root.tileImages=next}function onTileFailed(){root.tileFailures++}}
    ColumnLayout {
        anchors.fill:parent;anchors.margins:18;spacing:10
        RowLayout {Layout.fillWidth:true
            PlainLabel {Layout.fillWidth:true;text:"Local weather map · "+root.location;font.pixelSize:22;font.bold:true;elide:Text.ElideRight}
            ActionButton {objectName:"closeWeatherMap";text:"Close";onClicked:root.closeRequested()}
        }
        RowLayout {Layout.fillWidth:true
            ActionButton {objectName:"mapTemperature";text:"Temperature";selected:root.mapLayer==="temperature";onClicked:root.mapLayer="temperature"}
            ActionButton {objectName:"mapWind";text:"Wind";selected:root.mapLayer==="wind";onClicked:root.mapLayer="wind"}
            ActionButton {objectName:"mapPrecipitation";text:"Forecast precipitation";selected:root.mapLayer==="precipitation";onClicked:root.mapLayer="precipitation"}
        }
        Item {id:mapArea;objectName:"mapArea";Layout.fillWidth:true;Layout.fillHeight:true;clip:true
            Rectangle {anchors.fill:parent;color:"#dce8e7"}
            Repeater {model:root.tiles();delegate:Image {
                required property var modelData
                x:modelData.left;y:modelData.top;width:modelData.size;height:modelData.size
                source:root.tileImages[modelData.key]||"";fillMode:Image.Stretch;asynchronous:true
                Component.onCompleted:mapTiles.request(root.zoom,modelData.requestX,modelData.y,root.mapState.offline)
            }}
            Canvas {id:overlay;anchors.fill:parent;onPaint:root.paint()}
            Rectangle {x:parent.width/2-root.desiredScale*16093.44;y:parent.height/2-root.desiredScale*16093.44;width:2*root.desiredScale*16093.44;height:width;radius:width/2;color:"transparent";border.color:"#efffffff";border.width:2}
            Rectangle {width:8;height:8;radius:4;color:"#ffffff";border.color:"#23435c";x:parent.width/2-4;y:parent.height/2-4}
            Rectangle {anchors.top:parent.top;anchors.right:parent.right;anchors.margins:7;width:missingTiles.implicitWidth+14;height:missingTiles.implicitHeight+8;radius:5;color:"#eaf5fa";visible:root.mapData&&root.tileFailures>=root.tiles().length&&Object.keys(root.tileImages).length===0
                PlainLabel {id:missingTiles;anchors.centerIn:parent;text:"Geographic background unavailable";font.pixelSize:12;color:"#163448"}
            }
            Rectangle {anchors.left:parent.left;anchors.bottom:parent.bottom;anchors.margins:6;width:mapCredit.width+12;height:mapCredit.height+6;radius:4;color:"#edf7fb"
                PlainLabel {id:mapCredit;anchors.centerIn:parent;text:"10-mile radius · © OpenStreetMap contributors (ODbL)";font.pixelSize:11;color:"#132f43"}
                MouseArea {anchors.fill:parent;cursorShape:Qt.PointingHandCursor;onClicked:Qt.openUrlExternally("https://www.openstreetmap.org/copyright")}
            }
            Rectangle {anchors.fill:parent;visible:!root.mapData;color:"#dce8e7";opacity:0.9}
            PlainLabel {anchors.centerIn:parent;visible:!root.mapData;text:root.mapState.status==="loading"?"Loading local model forecast…":root.mapState.offline?"Map unavailable offline for this location":"Map forecast unavailable";color:"#233e54"}
        }
        PlainLabel {Layout.fillWidth:true;text:root.mapData?(root.mapLayer==="temperature"?"Temperature · "+root.temperature(root.mapData.cells[12].temperature_c[root.hourIndex]):root.mapLayer==="wind"?"Wind from direction · "+root.windSpeed(root.mapData.cells[12].wind_speed_m_s[root.hourIndex]):"Forecast precipitation in preceding hour · "+root.rain(root.mapData.cells[12].precipitation_mm[root.hourIndex])+" · No shade = 0"):"";font.pixelSize:14}
        RowLayout {Layout.fillWidth:true;visible:root.mapData!==null
            PlainLabel {text:"Legend";font.pixelSize:12;color:Tokens.secondary}
            PlainLabel {visible:root.mapLayer!=="wind";text:root.mapLayer==="temperature"?root.temperature(root.fieldMin):root.rain(root.fieldMin);font.pixelSize:12}
            Rectangle {visible:root.mapLayer!=="wind";Layout.preferredWidth:160;Layout.preferredHeight:12;radius:3
                gradient:Gradient {orientation:Gradient.Horizontal;GradientStop {position:0;color:root.shade(0,0,1)}GradientStop {position:1;color:root.shade(1,0,1)}}}
            PlainLabel {visible:root.mapLayer!=="wind";text:root.mapLayer==="temperature"?root.temperature(root.fieldMax):root.rain(root.fieldMax);font.pixelSize:12}
            PlainLabel {visible:root.mapLayer==="wind";text:"Arrow points where wind blows · labels show "+(root.units==="F"?"mph":"m/s");font.pixelSize:12;color:Tokens.secondary}
        }
        RowLayout {Layout.fillWidth:true
            ActionButton {objectName:"mapPreviousHour";text:"Previous";enabled:root.mapData&&root.hourIndex>0;onClicked:root.hourIndex--}
            Slider {id:timeline;objectName:"mapTimeline";Layout.fillWidth:true;from:0;to:root.mapData?root.mapData.hours.length-1:0;stepSize:1;value:root.hourIndex;enabled:root.mapData&&root.mapData.hours.length>1;onMoved:root.hourIndex=Math.round(value)}
            ActionButton {objectName:"mapNextHour";text:"Next";enabled:root.mapData&&root.hourIndex<root.mapData.hours.length-1;onClicked:root.hourIndex++}
        }
        PlainLabel {Layout.fillWidth:true;text:root.mapData?"Forecast valid "+root.mapState.hour_labels[root.hourIndex]+" · Hour "+(root.hourIndex+1)+" of "+root.mapData.hours.length:"";font.pixelSize:14}
        PlainLabel {Layout.fillWidth:true;text:root.mapData?root.mapData.model_name+" · native grid ~"+root.mapData.resolution_km+" km · sampled/interpolated forecast · fetched "+root.mapState.fetched_label+(root.mapState.status==="stale"?" · stale":"")+(root.mapState.offline?" · offline":""):"";font.pixelSize:12;color:Tokens.secondary;wrapMode:Text.Wrap}
        PlainLabel {Layout.fillWidth:true;text:root.mapData?root.mapData.attribution+(root.mapData.resolution_km>=10?" · Coarse global pattern; neighborhood detail unavailable.":""):"";font.pixelSize:11;color:Tokens.secondary;wrapMode:Text.Wrap}
    }
}
