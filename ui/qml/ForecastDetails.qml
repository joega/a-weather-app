import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Forecast.js" as Forecast

Popup {
    id: root
    objectName: "forecastDetails"
    parent: Overlay.overlay
    anchors.centerIn: parent
    width: Math.min(760, parent ? parent.width-40 : 760)
    height: Math.min(730, parent ? parent.height-40 : 730)
    padding: 24
    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    property var forecast: null
    property string units: "F"
    property string freshness: ""
    property string selectedDate: ""
    property string selectedTime: ""
    property string metric: "temperature_c"
    readonly property var day: forecast && selectedDate ? forecast.daily.find(d=>d.date===selectedDate)||null : null
    readonly property var hours: !forecast?[]:selectedDate?forecast.hourly.filter(h=>h.local_date===selectedDate):forecast.hourly.slice(0,24)
    readonly property int selectedIndex: Math.max(0,hours.findIndex(h=>h.time===selectedTime))
    readonly property var hour: hours.length?hours[selectedIndex]:null
    function showHour(row) { selectedDate="";selectedTime=row.time;metric="temperature_c";open() }
    function showDay(row) { selectedDate=row.date;selectedTime="";metric="temperature_c";open() }
    onOpened: { closeButton.forceActiveFocus();if(scroll.contentItem)scroll.contentItem.contentY=0 }
    background: Rectangle { color:"#2c455a";radius:18;border.color:Tokens.border }
    Overlay.modal: Rectangle { color:"#990b1725" }
    contentItem: ScrollView {
        id:scroll
        objectName:"forecastDetailsScroll"
        clip:true;contentWidth:availableWidth;contentHeight:detailBody.implicitHeight
        ScrollBar.vertical.policy: ScrollBar.AsNeeded
        ScrollBar.vertical.active: true
        ScrollBar.horizontal.policy: ScrollBar.AlwaysOff
        ColumnLayout {
            id:detailBody
            width:scroll.availableWidth;spacing:16
            RowLayout {
                Layout.fillWidth:true
                PlainLabel { Layout.fillWidth:true;text:root.day?root.day.day_label+" · "+root.day.date:"Hourly forecast";font.pixelSize:26;wrapMode:Text.Wrap;elide:Text.ElideNone }
                ActionButton { id:closeButton;objectName:"closeForecastDetails";iconName:"close";accessibleLabel:"Close forecast details";onClicked:root.close() }
            }
            PlainLabel { Layout.fillWidth:true;text:root.freshness;color:Tokens.secondary;font.pixelSize:13;wrapMode:Text.Wrap;elide:Text.ElideNone }
            PlainLabel {
                visible:root.day!==null;Layout.fillWidth:true;font.pixelSize:19;wrapMode:Text.Wrap;elide:Text.ElideNone
                text:root.day?Forecast.title(root.day.condition)+" · High "+Forecast.temp(root.day.high_c,root.units)+" · Low "+Forecast.temp(root.day.low_c,root.units)+"\nPrecipitation chance "+Forecast.percent(root.day.precipitation_probability):""
            }
            PlainLabel { visible:root.day!==null;Layout.fillWidth:true;text:root.day?"Sunrise "+(root.day.sunrise_label||"—")+" · Sunset "+(root.day.sunset_label||"—"):"";font.pixelSize:14;color:Tokens.secondary;wrapMode:Text.Wrap;elide:Text.ElideNone }
            ChoiceControl {
                Layout.fillWidth:true;testName:"forecastMetric";value:root.metric
                choices:[{label:"Temperature",value:"temperature_c"},{label:"Precipitation",value:"precipitation_rate_mm_hr"},{label:"Wind",value:"wind_speed_m_s"}]
                onChosen:value=>root.metric=value
            }
            ForecastChart { Layout.fillWidth:true;hours:root.hours;units:root.units;metric:root.metric;selectedIndex:root.selectedIndex;onSelected:index=>root.selectedTime=root.hours[index].time }
            PlainLabel { Layout.fillWidth:true;text:root.metric==="precipitation_rate_mm_hr"?"Precipitation total for the hour ending at each time.":"Select an hour to explore its model forecast. Only remaining forecast hours are shown.";font.pixelSize:13;color:Tokens.secondary;wrapMode:Text.Wrap;elide:Text.ElideNone }
            RowLayout {
                Layout.fillWidth:true
                ActionButton { objectName:"previousForecastHour";text:"Previous";enabled:root.selectedIndex>0;onClicked:root.selectedTime=root.hours[root.selectedIndex-1].time }
                PlainLabel { objectName:"selectedForecastHour";Layout.fillWidth:true;text:root.hour?root.hour.local_label:"Hourly detail unavailable";horizontalAlignment:Text.AlignHCenter;font.pixelSize:16;wrapMode:Text.Wrap;elide:Text.ElideNone }
                ActionButton { objectName:"nextForecastHour";text:"Next";enabled:root.selectedIndex<root.hours.length-1;onClicked:root.selectedTime=root.hours[root.selectedIndex+1].time }
            }
            PlainLabel { visible:root.hour!==null;Layout.fillWidth:true;text:root.hour?Forecast.title(root.hour.condition)+" · "+Forecast.temp(root.hour.temperature_c,root.units)+" · Feels like "+Forecast.temp(root.hour.apparent_temperature_c,root.units):"";font.pixelSize:22;wrapMode:Text.Wrap;elide:Text.ElideNone }
            GridLayout {
                visible:root.hour!==null;Layout.fillWidth:true;columns:2;columnSpacing:24;rowSpacing:14
                Repeater {
                    model:root.hour?[
                        {title:"Precipitation chance",value:Forecast.percent(root.hour.precipitation_probability)},
                        {title:"Precipitation amount",value:Forecast.amount(root.hour.precipitation_rate_mm_hr,root.units)},
                        {title:"Wind",value:Forecast.direction(root.hour.wind_direction_deg)+" "+Forecast.wind(root.hour.wind_speed_m_s,root.units)},
                        {title:"Gusts",value:Forecast.wind(root.hour.wind_gust_m_s,root.units)},
                        {title:"Humidity",value:Forecast.percent(root.hour.humidity)},
                        {title:"Cloud cover",value:Forecast.percent(root.hour.cloud_cover)},
                        {kind:"uv",title:"UV index",value:Forecast.uv(root.hour.uv_index)},
                        {kind:"pressure",title:"Mean sea level pressure",value:Forecast.pressure(root.hour.pressure_msl_hpa)},
                        {kind:"dew_point",title:"Dew point",value:Forecast.temp(root.hour.dew_point_c,root.units)}]:[]
                    delegate:ColumnLayout {
                        required property var modelData
                        Layout.fillWidth:true;Layout.preferredWidth:1;spacing:4
                        PlainLabel { Layout.fillWidth:true;text:modelData.title;font.pixelSize:13;color:Tokens.secondary;wrapMode:Text.Wrap;elide:Text.ElideNone }
                        PlainLabel { objectName:modelData.kind?"detailMetricValue_"+modelData.kind:"";Layout.fillWidth:true;text:modelData.value;font.pixelSize:20;wrapMode:Text.Wrap;elide:Text.ElideNone }
                    }
                }
            }
            PlainLabel { visible:root.hour!==null;Layout.fillWidth:true;text:root.hour?"Precipitation interval: "+(root.hour.period_label||"the preceding hour")+". Times are local to the forecast location.":"";font.pixelSize:12;color:Tokens.secondary;wrapMode:Text.Wrap;elide:Text.ElideNone }
        }
    }
}
