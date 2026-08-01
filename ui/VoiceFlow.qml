import QtQuick
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import Quickshell.Wayland

// A deliberately small, independent indicator. It polls the local daemon only;
// it never receives microphone data, transcripts, or API credentials.
Scope {
    id: root
    property string phase: "idle"
    property string message: ""
    readonly property bool visibleIndicator: phase === "recording" || phase === "processing" || phase === "queued" || phase === "error"

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
        anchors { top: true }
        exclusionMode: ExclusionMode.Ignore
        exclusiveZone: 0
        margins.top: 42
        implicitWidth: pill.implicitWidth
        implicitHeight: pill.implicitHeight

        Rectangle {
            id: pill
            implicitWidth: row.implicitWidth + 28
            implicitHeight: 36
            radius: 18
            color: root.phase === "recording" ? "#d9534f" : root.phase === "error" ? "#9b3b3b" : "#2a2d36"
            border.color: "#ffffff22"
            border.width: 1

            RowLayout {
                id: row
                anchors.centerIn: parent
                spacing: 9
                Rectangle {
                    width: 9; height: 9; radius: 5
                    color: "#ffffff"
                    opacity: root.phase === "recording" ? pulse.opacity : 0.85
                    SequentialAnimation on opacity {
                        id: pulse
                        running: root.phase === "recording"
                        loops: Animation.Infinite
                        NumberAnimation { to: 0.35; duration: 520 }
                        NumberAnimation { to: 1; duration: 520 }
                    }
                }
                Text {
                    text: root.phase === "recording" ? "Listening" : root.message
                    color: "white"
                    font.pixelSize: 13
                    font.weight: Font.Medium
                }
            }
        }
    }
}
