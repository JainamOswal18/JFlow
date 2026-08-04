import QtQuick
import QtQuick.Layouts
import Quickshell
import Quickshell.Io

// The Library is intentionally a short-lived, normal desktop window. It reads
// only local daemon responses; it never sees microphone audio or credentials.
Scope {
FloatingWindow {
    id: root
    title: "JFlow"
    visible: true
    implicitWidth: 920
    implicitHeight: 650
    minimumSize.width: 680
    minimumSize.height: 460
    color: "#0b0b0b"

    property int page: 0
    property var jobs: []
    property var vocabulary: []
    property string search: ""
    property string feedback: ""
    property string actionMessage: ""

    // Quickshell often starts with a restricted graphical PATH. Use a small
    // POSIX-shell wrapper so the Library always reaches JFlow's installed
    // per-user binary without baking a username into this distributed QML.
    function daemonCommand(args) {
        return ["/bin/sh", "-c", "exec ~/.local/bin/dictationd \"$@\"", "jflow"].concat(args)
    }

    function refreshHistory() {
        if (historyProc.running) return
        historyProc.command = search.length > 0 ? daemonCommand(["history", search]) : daemonCommand(["history"])
        historyProc.running = true
    }

    function refreshVocabulary() {
        if (!vocabularyProc.running)
            vocabularyProc.running = true
    }

    function runAction(command, message) {
        if (actionProc.running) return
        actionMessage = message
        actionProc.command = command
        actionProc.running = true
    }

    function addVocabulary() {
        var canonical = vocabularyInput.text.trim()
        if (canonical.length === 0) {
            feedback = "Enter a word or phrase first"
            return
        }
        runAction(daemonCommand(["vocabulary-add", canonical]), "Added to Scribe vocabulary")
    }

    function correctHistory(job, text) {
        var corrected = text.trim()
        if (corrected.length === 0) {
            feedback = "Corrected text cannot be empty"
            return
        }
        runAction(daemonCommand(["correct-history", job.id, corrected]), "Saved correction and learned local aliases")
    }

    function preview(job) {
        var text = job.final_text || job.transcript || job.error || "No text was saved"
        return text.replace(/\s+/g, " ").trim()
    }

    function timestamp(value) {
        if (!value) return ""
        return new Date(value).toLocaleString(Qt.locale(), "MMM d, h:mm AP")
    }

    function canRetry(job) {
        return job.status === "failed" || job.status === "retry_wait"
    }

    Component.onCompleted: {
        refreshHistory()
        refreshVocabulary()
    }

    Timer {
        id: searchDebounce
        interval: 240
        repeat: false
        onTriggered: root.refreshHistory()
    }

    Process {
        id: historyProc
        command: root.daemonCommand(["history"])
        stdout: StdioCollector {
            onStreamFinished: {
                try {
                    var reply = JSON.parse(text)
                    if (reply.ok) root.jobs = reply.jobs || []
                    else root.feedback = reply.error || "Could not load history"
                } catch (_) { root.feedback = "Could not read history" }
            }
        }
        stderr: StdioCollector {
            onStreamFinished: { if (text.trim().length > 0) root.feedback = text.trim() }
        }
    }

    Process {
        id: vocabularyProc
        command: root.daemonCommand(["vocabulary"])
        stdout: StdioCollector {
            onStreamFinished: {
                try {
                    var reply = JSON.parse(text)
                    if (reply.ok) root.vocabulary = reply.vocabulary || []
                    else root.feedback = reply.error || "Could not load vocabulary"
                } catch (_) { root.feedback = "Could not read vocabulary" }
            }
        }
        stderr: StdioCollector {
            onStreamFinished: { if (text.trim().length > 0) root.feedback = text.trim() }
        }
    }

    Process {
        id: actionProc
        command: ["true"]
        stdout: StdioCollector {
            onStreamFinished: {
                try {
                    var reply = JSON.parse(text)
                    if (!reply.ok) {
                        root.feedback = reply.error || "Action failed"
                        return
                    }
                    root.feedback = root.actionMessage
                    vocabularyInput.text = ""
                    root.refreshHistory()
                    root.refreshVocabulary()
                } catch (_) { root.feedback = "Action failed" }
            }
        }
        stderr: StdioCollector {
            onStreamFinished: { if (text.trim().length > 0) root.feedback = text.trim() }
        }
    }

    Rectangle {
        anchors.fill: parent
        color: "#0b0b0b"

        ColumnLayout {
            anchors.fill: parent
            anchors.margins: 26
            spacing: 18

            RowLayout {
                Layout.fillWidth: true

                ColumnLayout {
                    spacing: 2
                    Text { text: "JFlow"; color: "#ffffff"; font.pixelSize: 28; font.weight: Font.DemiBold }
                    Text {
                        text: root.page === 0 ? "Your local dictation history" : "Local corrections before text is inserted"
                        color: "#a8a8a8"
                        font.pixelSize: 13
                    }
                }
                Item { Layout.fillWidth: true }
                Rectangle {
                    implicitWidth: 34; implicitHeight: 34; radius: 17
                    color: closeArea.containsMouse ? "#ffffff" : "#202020"
                    Text { anchors.centerIn: parent; text: "×"; color: closeArea.containsMouse ? "#090909" : "#ffffff"; font.pixelSize: 22 }
                    // FloatingWindow has no `closing` signal. Terminate just
                    // this standalone Quickshell process so it cannot become
                    // a headless `--no-duplicate` blocker.
                    MouseArea { id: closeArea; anchors.fill: parent; hoverEnabled: true; onClicked: Quickshell.execDetached(["/usr/bin/kill", "-TERM", String(Quickshell.processId)]) }
                }
            }

            RowLayout {
                Layout.fillWidth: true
                spacing: 8
                Repeater {
                    model: ["History", "Vocabulary"]
                    delegate: Rectangle {
                        required property var modelData
                        required property int index
                        implicitWidth: label.implicitWidth + 30; implicitHeight: 34; radius: 17
                        color: root.page === index ? "#ffffff" : "#191919"
                        Text { id: label; anchors.centerIn: parent; text: modelData; color: root.page === index ? "#090909" : "#d0d0d0"; font.pixelSize: 13; font.weight: Font.DemiBold }
                        MouseArea { anchors.fill: parent; onClicked: root.page = index }
                    }
                }
                Item { Layout.fillWidth: true }
                Text {
                    visible: root.feedback.length > 0
                    text: root.feedback
                    color: "#d6d6d6"
                    font.pixelSize: 12
                    elide: Text.ElideRight
                    Layout.maximumWidth: 260
                }
            }

            Rectangle { Layout.fillWidth: true; implicitHeight: 1; color: "#292929" }

            Item {
                Layout.fillWidth: true
                Layout.fillHeight: true

                Item {
                    anchors.fill: parent
                    visible: root.page === 0

                    ColumnLayout {
                        anchors.fill: parent
                        spacing: 14

                        Rectangle {
                            Layout.fillWidth: true; implicitHeight: 42; radius: 10
                            color: "#171717"; border.color: searchInput.activeFocus ? "#ffffff" : "#303030"; border.width: 1
                            TextInput {
                                id: searchInput
                                anchors.fill: parent; anchors.leftMargin: 15; anchors.rightMargin: 42
                                verticalAlignment: TextInput.AlignVCenter
                                color: "#ffffff"; font.pixelSize: 14; clip: true
                                onTextChanged: { root.search = text; searchDebounce.restart() }
                            }
                            Text {
                                anchors.verticalCenter: parent.verticalCenter; anchors.left: parent.left; anchors.leftMargin: 15
                                visible: searchInput.text.length === 0
                                text: "Search text, app, status, or error"
                                color: "#777777"; font.pixelSize: 14
                            }
                            Text { anchors.verticalCenter: parent.verticalCenter; anchors.right: parent.right; anchors.rightMargin: 14; text: "⌕"; color: "#c8c8c8"; font.pixelSize: 20 }
                        }

                        Text { text: jobs.length + (jobs.length === 1 ? " dictation" : " dictations"); color: "#8e8e8e"; font.pixelSize: 12 }

                        ListView {
                            id: historyList
                            Layout.fillWidth: true; Layout.fillHeight: true
                            clip: true; spacing: 9
                            model: root.jobs
                            delegate: Rectangle {
                                id: historyCard
                                required property var modelData
                                property bool editing: false
                                width: historyList.width; implicitHeight: content.implicitHeight + 28
                                radius: 12; color: "#151515"; border.color: "#282828"; border.width: 1
                                ColumnLayout {
                                    id: content
                                    anchors.fill: parent; anchors.margins: 14; spacing: 8
                                    RowLayout {
                                        Layout.fillWidth: true; spacing: 8
                                        Text { text: root.timestamp(modelData.created_at); color: "#a5a5a5"; font.pixelSize: 12 }
                                        Text { text: modelData.target && modelData.target.class ? modelData.target.class : "Unknown app"; color: "#777777"; font.pixelSize: 12; elide: Text.ElideRight; Layout.maximumWidth: 190 }
                                        Item { Layout.fillWidth: true }
                                        Rectangle {
                                            implicitWidth: stateLabel.implicitWidth + 16; implicitHeight: 22; radius: 11
                                            color: root.canRetry(modelData) ? "#40211f" : modelData.status === "delivered" ? "#1d3226" : "#242424"
                                            Text { id: stateLabel; anchors.centerIn: parent; text: String(modelData.status).replace("_", " "); color: "#e0e0e0"; font.pixelSize: 10; font.capitalization: Font.AllUppercase }
                                        }
                                    }
                                    Text {
                                        Layout.fillWidth: true
                                        visible: !historyCard.editing
                                        text: root.preview(modelData); color: "#f2f2f2"; font.pixelSize: 14
                                        wrapMode: Text.Wrap; maximumLineCount: 3; elide: Text.ElideRight
                                    }
                                    Rectangle {
                                        visible: historyCard.editing
                                        Layout.fillWidth: true; implicitHeight: 64; radius: 8
                                        color: "#101010"; border.color: "#ffffff"; border.width: 1
                                        TextEdit {
                                            id: correctedInput
                                            anchors.fill: parent; anchors.margins: 10
                                            color: "#ffffff"; font.pixelSize: 14; wrapMode: TextEdit.Wrap
                                            selectByMouse: true
                                            text: root.preview(modelData)
                                        }
                                    }
                                    RowLayout {
                                        Layout.fillWidth: true; spacing: 7
                                        Item { Layout.fillWidth: true }
                                        ActionButton { label: "Copy"; onClicked: root.runAction(root.daemonCommand(["copy", modelData.id]), "Copied to clipboard") }
                                        ActionButton { visible: root.canRetry(modelData); label: "Retry"; onClicked: root.runAction(root.daemonCommand(["retry", modelData.id]), "Retry queued") }
                                        ActionButton { visible: !historyCard.editing; label: "Correct"; onClicked: historyCard.editing = true }
                                        ActionButton { visible: historyCard.editing; label: "Cancel"; onClicked: historyCard.editing = false }
                                        ActionButton { visible: historyCard.editing; label: "Save"; prominent: true; onClicked: { root.correctHistory(modelData, correctedInput.text); historyCard.editing = false } }
                                        ActionButton { label: "Delete"; destructive: true; onClicked: root.runAction(root.daemonCommand(["delete-history", modelData.id]), "Dictation deleted") }
                                    }
                                }
                            }
                            Text {
                                anchors.centerIn: parent
                                visible: historyList.count === 0
                                text: root.search.length > 0 ? "No matching dictations" : "Your dictations will appear here"
                                color: "#777777"; font.pixelSize: 14
                            }
                        }
                    }
                }

                Item {
                    anchors.fill: parent
                    visible: root.page === 1
                    ColumnLayout {
                        anchors.fill: parent
                        spacing: 14
                        Text { text: "Add a name, product, or technical term"; color: "#9a9a9a"; font.pixelSize: 12 }
                        Rectangle {
                            Layout.fillWidth: true; implicitHeight: 42; radius: 10; color: "#171717"; border.color: vocabularyInput.activeFocus ? "#ffffff" : "#303030"; border.width: 1
                            TextInput { id: vocabularyInput; anchors.fill: parent; anchors.leftMargin: 15; anchors.rightMargin: 15; verticalAlignment: TextInput.AlignVCenter; color: "#ffffff"; font.pixelSize: 14; onAccepted: root.addVocabulary() }
                            Text { anchors.verticalCenter: parent.verticalCenter; anchors.left: parent.left; anchors.leftMargin: 15; visible: vocabularyInput.text.length === 0; text: "e.g. Jainam Oswal or Hyprland"; color: "#777777"; font.pixelSize: 14 }
                        }
                        RowLayout {
                            Layout.fillWidth: true; spacing: 10
                            Item { Layout.fillWidth: true }
                            ActionButton { label: "Add to Scribe"; prominent: true; onClicked: root.addVocabulary() }
                        }
                        Text { text: "Scribe receives only this spelling. JFlow learns likely mistakes from History corrections on your device."; color: "#737373"; font.pixelSize: 12; wrapMode: Text.Wrap; Layout.fillWidth: true }
                        Rectangle { Layout.fillWidth: true; implicitHeight: 1; color: "#292929" }
                        ListView {
                            id: vocabularyList
                            Layout.fillWidth: true; Layout.fillHeight: true
                            clip: true; spacing: 8; model: root.vocabulary
                            delegate: Rectangle {
                                required property var modelData
                                width: vocabularyList.width; implicitHeight: 58; radius: 10; color: "#151515"; border.color: "#282828"; border.width: 1
                                RowLayout {
                                    anchors.fill: parent; anchors.margins: 13; spacing: 12
                                    ColumnLayout {
                                        Layout.fillWidth: true; spacing: 3
                                        Text { text: modelData.canonical; color: "#ffffff"; font.pixelSize: 15; font.weight: Font.DemiBold; elide: Text.ElideRight; Layout.fillWidth: true }
                                        Text { text: (modelData.aliases || []).length + " local learned " + ((modelData.aliases || []).length === 1 ? "alias" : "aliases"); color: "#b0b0b0"; font.pixelSize: 12; elide: Text.ElideRight; Layout.fillWidth: true }
                                    }
                                    ActionButton { label: "Delete"; destructive: true; onClicked: root.runAction(root.daemonCommand(["vocabulary-delete", modelData.id]), "Correction deleted") }
                                }
                            }
                            Text { anchors.centerIn: parent; visible: vocabularyList.count === 0; text: "Add names and terms JFlow should write correctly"; color: "#777777"; font.pixelSize: 14 }
                        }
                    }
                }
            }
        }
    }

    component ActionButton: Rectangle {
        property string label: ""
        property bool destructive: false
        property bool prominent: false
        signal clicked()
        implicitWidth: buttonLabel.implicitWidth + 24
        implicitHeight: 28
        radius: 14
        color: prominent ? "#ffffff" : buttonArea.containsMouse ? "#303030" : "#202020"
        border.color: destructive ? "#6a302c" : "#363636"
        border.width: destructive ? 1 : 0
        visible: true
        Text { id: buttonLabel; anchors.centerIn: parent; text: parent.label; color: parent.prominent ? "#090909" : parent.destructive ? "#ffb5af" : "#e9e9e9"; font.pixelSize: 11; font.weight: Font.DemiBold }
        MouseArea { id: buttonArea; anchors.fill: parent; hoverEnabled: true; onClicked: parent.clicked() }
    }
}
}
