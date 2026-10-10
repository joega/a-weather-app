import QtQuick

Text {
    property bool skyText: false
    textFormat: Text.PlainText
    style: skyText ? Text.Raised : Text.Normal
    styleColor: "#b00b1c30"
    color: Tokens.foreground
    font.family: "sans-serif"
    font.pixelSize: Tokens.fontSize(17)
    elide: Text.ElideRight
}
