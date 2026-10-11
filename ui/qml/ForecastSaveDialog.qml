import QtQuick.Dialogs

FileDialog {
    title: I18n.t("Save forecast image")
    fileMode: FileDialog.SaveFile
    nameFilters: [I18n.t("PNG image (*.png)")]
    defaultSuffix: "png"
    signal chosen(url destination)
    signal finished
    onAccepted: {
        chosen(selectedFile);
        finished();
    }
    onRejected: finished()
}
