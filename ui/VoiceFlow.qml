import QtQuick
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import Quickshell.Wayland

// A small, independent indicator. It polls the local daemon only; it never
// receives microphone data, transcripts, or API credentials.
Scope {
    id: root
    property string phase: "idle"
    property string message: ""
    readonly property bool visibleIndicator: phase === "recording" || phase === "processing" || phase === "retrying" || phase === "queued" || phase === "error"
    readonly property bool recording: phase === "recording"
    readonly property bool working: phase === "processing" || phase === "retrying" || phase === "queued"

    function label() {
        if (recording) return "Listening"
        if (phase === "retrying") return message.length > 0 ? message : "Retrying"
        if (phase === "queued") return "Saved locally"
        if (phase === "processing") return message.length > 0 ? message : "Processing"
        return message
    }

    Timer {
        interval: 180
        running: true
        repeat: true
        onTriggered: {
            if (!statusProc.running)
                statusProc.running = true
        }
    }

    Process {
        id: statusProc
        command: ["dictationd", "status"]
        stdout: StdioCollector {
            onStreamFinished: {
                try {
                    const reply = JSON.parse(text)
                    root.phase = reply.status.phase || "idle"
                    root.message = reply.status.message || ""
                } catch (_) { }
            }
        }
    }

    PanelWindow {
        id: indicator
        visible: root.visibleIndicator
        color: "transparent"
        WlrLayershell.namespace: "dictationd-indicator"
        WlrLayershell.layer: WlrLayer.Overlay
        anchors { bottom: true }
        exclusionMode: ExclusionMode.Ignore
        exclusiveZone: 0
        margins.bottom: 48
        implicitWidth: pill.implicitWidth
        implicitHeight: pill.implicitHeight

        Rectangle {
            id: pill
            implicitWidth: row.implicitWidth + 34
            implicitHeight: 42
            radius: 21
            color: "#090909"
            border.color: "#ffffff"
            border.width: 1

            opacity: root.visibleIndicator ? 1 : 0
            scale: root.visibleIndicator ? 1 : 0.92
            Behavior on opacity { NumberAnimation { duration: 140 } }
            Behavior on scale { NumberAnimation { duration: 170; easing.type: Easing.OutCubic } }

            RowLayout {
                id: row
                anchors.centerIn: parent
                spacing: 10
                Rectangle {
                    width: 18; height: 18; radius: 9
                    color: "transparent"
                    border.color: "#ffffff"
                    border.width: root.recording ? 1 : 0
                    opacity: root.recording ? pulse.opacity : 1
                    SequentialAnimation on opacity {
                        id: pulse; running: root.recording; loops: Animation.Infinite
                        NumberAnimation { to: 0.25; duration: 650 }
                        NumberAnimation { to: 1; duration: 650 }
                    }
                }
                Rectangle {
                    width: 7; height: 7; radius: 4
                    color: "#ffffff"
                    visible: root.recording
                    Layout.leftMargin: -22
                }
                Text {
                    text: root.working ? "◌" : root.phase === "error" ? "!" : ""
                    visible: !root.recording
                    color: "#ffffff"
                    font.pixelSize: 20
                    font.weight: Font.Light
                    RotationAnimator on rotation {
                        running: root.working
                        from: 0; to: 360; duration: 950
                        loops: Animation.Infinite
                    }
                }
                Text {
                    text: root.label()
                    color: "white"
                    font.pixelSize: 13
                    font.weight: Font.DemiBold
                    font.letterSpacing: 0.3
                }
            }
        }
    }
}