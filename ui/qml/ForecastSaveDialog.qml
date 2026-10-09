import QtQuick.Dialogs

FileDialog {
    title: "Save forecast image"
    fileMode: FileDialog.SaveFile
    nameFilters: ["PNG image (*.png)"]
    defaultSuffix: "png"
    signal chosen(url destination)
    signal finished
    onAccepted: {
        chosen(selectedFile);
        finished();
    }
    onRejected: finished()
}
