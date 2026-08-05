import QtQuick
import QtQuick.Layouts
import Quickshell
import Quickshell.Io

// The Library is intentionally a short-lived, normal desktop window. It reads
// only local daemon responses; it never sees microphone audio or credentials.
Scope {
    FloatingWindow {
        id: root

        property int page: 0
        property var jobs: []
        property var vocabulary: []
        property string search: ""
        property string feedback: ""
        property string actionMessage: ""
        property string auditJobID: ""

        // Quickshell often starts with a restricted graphical PATH. Use a small
        // POSIX-shell wrapper so the Library always reaches JFlow's installed
        // per-user binary without baking a username into this distributed QML.
        function daemonCommand(args) {
            return ["/bin/sh", "-c", "exec ~/.local/bin/dictationd \"$@\"", "jflow"].concat(args);
        }

        function refreshHistory() {
            if (historyProc.running)
                return ;

            historyProc.command = search.length > 0 ? daemonCommand(["history", search]) : daemonCommand(["history"]);
            historyProc.running = true;
        }

        function refreshVocabulary() {
            if (!vocabularyProc.running)
                vocabularyProc.running = true;

        }

        function runAction(command, message) {
            if (actionProc.running)
                return ;

            actionMessage = message;
            actionProc.command = command;
            actionProc.running = true;
        }

        function addVocabulary() {
            var canonical = vocabularyInput.text.trim();
            if (canonical.length === 0) {
                feedback = "Enter a word or phrase first";
                return ;
            }
            runAction(daemonCommand(["vocabulary-add", canonical]), "Added to Scribe vocabulary");
        }

        function correctHistory(job, text) {
            var corrected = text.trim();
            if (corrected.length === 0) {
                feedback = "Corrected text cannot be empty";
                return ;
            }
            runAction(daemonCommand(["correct-history", job.id, corrected]), "Saved correction and learned local aliases");
        }

        function learnSelectedCorrection() {
            runAction(daemonCommand(["learn-selection"]), "Learned selected vocabulary");
        }

        function setFormatterFeedback(job, value) {
            runAction(daemonCommand(["formatter-feedback", job.id, value]), value === "helpful" ? "Marked useful" : "Marked for improvement");
        }

        function auditJob() {
            for (var i = 0; i < jobs.length; ++i) if (jobs[i].id === auditJobID) {
                return jobs[i];
            }
            for (var j = 0; j < jobs.length; ++j) if (jobs[j].formatting && jobs[j].formatting.eligible) {
                return jobs[j];
            }
            return null;
        }

        function auditDetails(job) {
            if (!job || !job.formatting)
                return "Choose a formatted dictation from the list.";

            var f = job.formatting;
            var lines = [];
            lines.push("SCRIBE INPUT\n" + (f.input_text || job.transcript || "Not recorded"));
            lines.push("FINAL INSERTED TEXT\n" + (job.final_text || "Not recorded"));
            lines.push("FORMATTER\n" + (f.model || "Unknown model") + " · " + (f.latency_ms || 0) + " ms · HTTP " + (f.http_status || "n/a"));
            lines.push("CONTEXT HINT\n" + (f.context_hint || "Neutral"));
            lines.push("SYSTEM PROMPT\n" + (f.system_prompt || "Not recorded"));
            lines.push("RAW QWEN RESPONSE\n" + (f.raw_response || "No response captured"));
            if (f.preprocess_rules && f.preprocess_rules.length)
                lines.push("LOCAL PREPROCESS\n" + f.preprocess_rules.join(", "));

            if (f.skipped)
                lines.push("FORMATTER ERROR\n" + f.skipped);

            lines.push("FEEDBACK\n" + (f.feedback || "Not reviewed"));
            return lines.join("\n\n");
        }

        function usageSummary() {
            var cloud = 0, local = 0, formatted = 0, latency = 0, latencyCount = 0;
            for (var i = 0; i < jobs.length; ++i) {
                var job = jobs[i];
                if (job.usage && job.usage.audio_seconds) {
                    if (job.usage.cloud)
                        cloud += job.usage.audio_seconds;
                    else
                        local += job.usage.audio_seconds;
                }
                if (job.formatting && job.formatting.eligible) {
                    formatted++;
                    if (job.formatting.latency_ms) {
                        latency += job.formatting.latency_ms;
                        latencyCount++;
                    }
                }
            }
            var line = "Recorded usage since this release: " + Math.round(cloud) + " s cloud ASR";
            if (local > 0)
                line += " · " + Math.round(local) + " s local ASR";

            line += "\nFormatter audits: " + formatted;
            if (latencyCount > 0)
                line += " · average Qwen latency: " + Math.round(latency / latencyCount) + " ms";

            return line;
        }

        function preview(job) {
            var text = job.final_text || job.transcript || job.error || "No text was saved";
            return text.replace(/\s+/g, " ").trim();
        }

        function timestamp(value) {
            if (!value)
                return "";

            return new Date(value).toLocaleString(Qt.locale(), "MMM d, h:mm AP");
        }

        function canRetry(job) {
            return job.status === "failed" || job.status === "retry_wait";
        }

        function formattingLabel(job) {
            if (!job.formatting || !job.formatting.eligible)
                return "";

            var hint = job.formatting.context_hint || "";
            var context = hint.indexOf("AI-assistant") >= 0 ? "AI prompt" : hint.indexOf("professional") >= 0 ? "Professional" : hint.indexOf("casual") >= 0 ? "Casual" : "Neutral";
            if (!job.formatting.applied)
                return context + " · original kept";

            return context + " · " + (job.formatting.changed ? "formatted" : "unchanged");
        }

        title: "JFlow"
        visible: true
        implicitWidth: 920
        implicitHeight: 650
        minimumSize.width: 680
        minimumSize.height: 460
        color: "#0b0b0b"
        Component.onCompleted: {
            refreshHistory();
            refreshVocabulary();
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
                        var reply = JSON.parse(text);
                        if (reply.ok)
                            root.jobs = reply.jobs || [];
                        else
                            root.feedback = reply.error || "Could not load history";
                    } catch (_) {
                        root.feedback = "Could not read history";
                    }
                }
            }

            stderr: StdioCollector {
                onStreamFinished: {
                    if (text.trim().length > 0)
                        root.feedback = text.trim();

                }
            }

        }

        Process {
            id: vocabularyProc

            command: root.daemonCommand(["vocabulary"])

            stdout: StdioCollector {
                onStreamFinished: {
                    try {
                        var reply = JSON.parse(text);
                        if (reply.ok)
                            root.vocabulary = reply.vocabulary || [];
                        else
                            root.feedback = reply.error || "Could not load vocabulary";
                    } catch (_) {
                        root.feedback = "Could not read vocabulary";
                    }
                }
            }

            stderr: StdioCollector {
                onStreamFinished: {
                    if (text.trim().length > 0)
                        root.feedback = text.trim();

                }
            }

        }

        Process {
            id: actionProc

            command: ["true"]

            stdout: StdioCollector {
                onStreamFinished: {
                    try {
                        var reply = JSON.parse(text);
                        if (!reply.ok) {
                            root.feedback = reply.error || "Action failed";
                            return ;
                        }
                        root.feedback = root.actionMessage;
                        vocabularyInput.text = "";
                        root.refreshHistory();
                        root.refreshVocabulary();
                    } catch (_) {
                        root.feedback = "Action failed";
                    }
                }
            }

            stderr: StdioCollector {
                onStreamFinished: {
                    if (text.trim().length > 0)
                        root.feedback = text.trim();

                }
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

                        Text {
                            text: "JFlow"
                            color: "#ffffff"
                            font.pixelSize: 28
                            font.weight: Font.DemiBold
                        }

                        Text {
                            text: root.page === 0 ? "Your local dictation history" : root.page === 1 ? "Local corrections before text is inserted" : root.page === 2 ? "Automatic local formatting for longer dictations" : "Formatter audits and provider usage"
                            color: "#a8a8a8"
                            font.pixelSize: 13
                        }

                    }

                    Item {
                        Layout.fillWidth: true
                    }

                    Rectangle {
                        implicitWidth: 34
                        implicitHeight: 34
                        radius: 17
                        color: closeArea.containsMouse ? "#ffffff" : "#202020"

                        Text {
                            anchors.centerIn: parent
                            text: "×"
                            color: closeArea.containsMouse ? "#090909" : "#ffffff"
                            font.pixelSize: 22
                        }
                        // FloatingWindow has no `closing` signal. Terminate just

                        // this standalone Quickshell process so it cannot become
                        // a headless `--no-duplicate` blocker.
                        MouseArea {
                            id: closeArea

                            anchors.fill: parent
                            hoverEnabled: true
                            onClicked: Quickshell.execDetached(["/usr/bin/kill", "-TERM", String(Quickshell.processId)])
                        }

                    }

                }

                RowLayout {
                    Layout.fillWidth: true
                    spacing: 8

                    Repeater {
                        model: ["History", "Vocabulary", "Formatting", "Insights"]

                        delegate: Rectangle {
                            required property var modelData
                            required property int index

                            implicitWidth: label.implicitWidth + 30
                            implicitHeight: 34
                            radius: 17
                            color: root.page === index ? "#ffffff" : "#191919"

                            Text {
                                id: label

                                anchors.centerIn: parent
                                text: modelData
                                color: root.page === index ? "#090909" : "#d0d0d0"
                                font.pixelSize: 13
                                font.weight: Font.DemiBold
                            }

                            MouseArea {
                                anchors.fill: parent
                                onClicked: root.page = index
                            }

                        }

                    }

                    Item {
                        Layout.fillWidth: true
                    }

                    Text {
                        visible: root.feedback.length > 0
                        text: root.feedback
                        color: "#d6d6d6"
                        font.pixelSize: 12
                        elide: Text.ElideRight
                        Layout.maximumWidth: 260
                    }

                }

                Rectangle {
                    Layout.fillWidth: true
                    implicitHeight: 1
                    color: "#292929"
                }

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
                                Layout.fillWidth: true
                                implicitHeight: 42
                                radius: 10
                                color: "#171717"
                                border.color: searchInput.activeFocus ? "#ffffff" : "#303030"
                                border.width: 1

                                TextInput {
                                    id: searchInput

                                    anchors.fill: parent
                                    anchors.leftMargin: 15
                                    anchors.rightMargin: 42
                                    verticalAlignment: TextInput.AlignVCenter
                                    color: "#ffffff"
                                    font.pixelSize: 14
                                    clip: true
                                    onTextChanged: {
                                        root.search = text;
                                        searchDebounce.restart();
                                    }
                                }

                                Text {
                                    anchors.verticalCenter: parent.verticalCenter
                                    anchors.left: parent.left
                                    anchors.leftMargin: 15
                                    visible: searchInput.text.length === 0
                                    text: "Search text, app, status, or error"
                                    color: "#777777"
                                    font.pixelSize: 14
                                }

                                Text {
                                    anchors.verticalCenter: parent.verticalCenter
                                    anchors.right: parent.right
                                    anchors.rightMargin: 14
                                    text: "⌕"
                                    color: "#c8c8c8"
                                    font.pixelSize: 20
                                }

                            }

                            Text {
                                text: root.jobs.length + (root.jobs.length === 1 ? " dictation" : " dictations")
                                color: "#8e8e8e"
                                font.pixelSize: 12
                            }

                            ListView {
                                id: historyList

                                Layout.fillWidth: true
                                Layout.fillHeight: true
                                clip: true
                                spacing: 9
                                model: root.jobs

                                Text {
                                    anchors.centerIn: parent
                                    visible: historyList.count === 0
                                    text: root.search.length > 0 ? "No matching dictations" : "Your dictations will appear here"
                                    color: "#777777"
                                    font.pixelSize: 14
                                }

                                delegate: Rectangle {
                                    id: historyCard

                                    required property var modelData
                                    property bool editing: false

                                    width: historyList.width
                                    implicitHeight: content.implicitHeight + 28
                                    radius: 12
                                    color: "#151515"
                                    border.color: "#282828"
                                    border.width: 1

                                    ColumnLayout {
                                        id: content

                                        anchors.fill: parent
                                        anchors.margins: 14
                                        spacing: 8

                                        RowLayout {
                                            Layout.fillWidth: true
                                            spacing: 8

                                            Text {
                                                text: root.timestamp(modelData.created_at)
                                                color: "#a5a5a5"
                                                font.pixelSize: 12
                                            }

                                            Text {
                                                text: modelData.target && modelData.target.class ? modelData.target.class : "Unknown app"
                                                color: "#777777"
                                                font.pixelSize: 12
                                                elide: Text.ElideRight
                                                Layout.maximumWidth: 190
                                            }

                                            Item {
                                                Layout.fillWidth: true
                                            }

                                            Rectangle {
                                                implicitWidth: stateLabel.implicitWidth + 16
                                                implicitHeight: 22
                                                radius: 11
                                                color: root.canRetry(modelData) ? "#40211f" : modelData.status === "delivered" ? "#1d3226" : "#242424"

                                                Text {
                                                    id: stateLabel

                                                    anchors.centerIn: parent
                                                    text: String(modelData.status).replace("_", " ")
                                                    color: "#e0e0e0"
                                                    font.pixelSize: 10
                                                    font.capitalization: Font.AllUppercase
                                                }

                                            }

                                        }

                                        Text {
                                            Layout.fillWidth: true
                                            visible: !historyCard.editing
                                            text: root.preview(modelData)
                                            color: "#f2f2f2"
                                            font.pixelSize: 14
                                            wrapMode: Text.Wrap
                                            maximumLineCount: 3
                                            elide: Text.ElideRight
                                        }

                                        Text {
                                            Layout.fillWidth: true
                                            visible: !historyCard.editing && root.formattingLabel(modelData).length > 0
                                            text: root.formattingLabel(modelData)
                                            color: modelData.formatting && modelData.formatting.applied ? "#9fc9ab" : "#cdbb8d"
                                            font.pixelSize: 11
                                        }

                                        Rectangle {
                                            visible: historyCard.editing
                                            Layout.fillWidth: true
                                            implicitHeight: 64
                                            radius: 8
                                            color: "#101010"
                                            border.color: "#ffffff"
                                            border.width: 1

                                            TextEdit {
                                                id: correctedInput

                                                anchors.fill: parent
                                                anchors.margins: 10
                                                color: "#ffffff"
                                                font.pixelSize: 14
                                                wrapMode: TextEdit.Wrap
                                                selectByMouse: true
                                                text: root.preview(modelData)
                                            }

                                        }

                                        RowLayout {
                                            Layout.fillWidth: true
                                            spacing: 7

                                            Item {
                                                Layout.fillWidth: true
                                            }

                                            ActionButton {
                                                label: "Copy"
                                                onClicked: root.runAction(root.daemonCommand(["copy", modelData.id]), "Copied to clipboard")
                                            }

                                            ActionButton {
                                                visible: root.canRetry(modelData)
                                                label: "Retry"
                                                onClicked: root.runAction(root.daemonCommand(["retry", modelData.id]), "Retry queued")
                                            }

                                            ActionButton {
                                                visible: modelData.formatting && modelData.formatting.eligible
                                                label: "Audit"
                                                onClicked: {
                                                    root.auditJobID = modelData.id;
                                                    root.page = 3;
                                                }
                                            }

                                            ActionButton {
                                                visible: modelData.formatting && modelData.formatting.eligible
                                                label: "Useful"
                                                onClicked: root.setFormatterFeedback(modelData, "helpful")
                                            }

                                            ActionButton {
                                                visible: modelData.formatting && modelData.formatting.eligible
                                                label: "Needs work"
                                                onClicked: root.setFormatterFeedback(modelData, "needs_work")
                                            }

                                            ActionButton {
                                                visible: !historyCard.editing
                                                label: "Correct"
                                                onClicked: historyCard.editing = true
                                            }

                                            ActionButton {
                                                visible: historyCard.editing
                                                label: "Cancel"
                                                onClicked: historyCard.editing = false
                                            }

                                            ActionButton {
                                                visible: historyCard.editing
                                                label: "Save"
                                                prominent: true
                                                onClicked: {
                                                    root.correctHistory(modelData, correctedInput.text);
                                                    historyCard.editing = false;
                                                }
                                            }

                                            ActionButton {
                                                label: "Delete"
                                                destructive: true
                                                onClicked: root.runAction(root.daemonCommand(["delete-history", modelData.id]), "Dictation deleted")
                                            }

                                        }

                                    }

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

                            Text {
                                text: "Add a name, product, or technical term"
                                color: "#9a9a9a"
                                font.pixelSize: 12
                            }

                            Rectangle {
                                Layout.fillWidth: true
                                implicitHeight: 42
                                radius: 10
                                color: "#171717"
                                border.color: vocabularyInput.activeFocus ? "#ffffff" : "#303030"
                                border.width: 1

                                TextInput {
                                    id: vocabularyInput

                                    anchors.fill: parent
                                    anchors.leftMargin: 15
                                    anchors.rightMargin: 15
                                    verticalAlignment: TextInput.AlignVCenter
                                    color: "#ffffff"
                                    font.pixelSize: 14
                                    onAccepted: root.addVocabulary()
                                }

                                Text {
                                    anchors.verticalCenter: parent.verticalCenter
                                    anchors.left: parent.left
                                    anchors.leftMargin: 15
                                    visible: vocabularyInput.text.length === 0
                                    text: "e.g. Jainam Oswal or Hyprland"
                                    color: "#777777"
                                    font.pixelSize: 14
                                }

                            }

                            RowLayout {
                                Layout.fillWidth: true
                                spacing: 10

                                ActionButton {
                                    label: "Learn selected correction"
                                    onClicked: root.learnSelectedCorrection()
                                }

                                Item {
                                    Layout.fillWidth: true
                                }

                                ActionButton {
                                    label: "Add to Scribe"
                                    prominent: true
                                    onClicked: root.addVocabulary()
                                }

                            }

                            Text {
                                text: "Scribe receives only this spelling. To learn an edit made in another app, select the corrected word or phrase there, then use Learn selected correction. JFlow compares it only with the latest dictation and learns close spelling variants locally."
                                color: "#737373"
                                font.pixelSize: 12
                                wrapMode: Text.Wrap
                                Layout.fillWidth: true
                            }

                            Rectangle {
                                Layout.fillWidth: true
                                implicitHeight: 1
                                color: "#292929"
                            }

                            ListView {
                                id: vocabularyList

                                Layout.fillWidth: true
                                Layout.fillHeight: true
                                clip: true
                                spacing: 8
                                model: root.vocabulary

                                Text {
                                    anchors.centerIn: parent
                                    visible: vocabularyList.count === 0
                                    text: "Add names and terms JFlow should write correctly"
                                    color: "#777777"
                                    font.pixelSize: 14
                                }

                                delegate: Rectangle {
                                    required property var modelData

                                    width: vocabularyList.width
                                    implicitHeight: 58
                                    radius: 10
                                    color: "#151515"
                                    border.color: "#282828"
                                    border.width: 1

                                    RowLayout {
                                        anchors.fill: parent
                                        anchors.margins: 13
                                        spacing: 12

                                        ColumnLayout {
                                            Layout.fillWidth: true
                                            spacing: 3

                                            Text {
                                                text: modelData.canonical
                                                color: "#ffffff"
                                                font.pixelSize: 15
                                                font.weight: Font.DemiBold
                                                elide: Text.ElideRight
                                                Layout.fillWidth: true
                                            }

                                            Text {
                                                text: (modelData.aliases || []).length + " local learned " + ((modelData.aliases || []).length === 1 ? "alias" : "aliases")
                                                color: "#b0b0b0"
                                                font.pixelSize: 12
                                                elide: Text.ElideRight
                                                Layout.fillWidth: true
                                            }

                                        }

                                        ActionButton {
                                            label: "Delete"
                                            destructive: true
                                            onClicked: root.runAction(root.daemonCommand(["vocabulary-delete", modelData.id]), "Correction deleted")
                                        }

                                    }

                                }

                            }

                        }

                    }

                    Item {
                        anchors.fill: parent
                        visible: root.page === 2

                        ColumnLayout {
                            anchors.fill: parent
                            spacing: 16

                            Text {
                                text: "Automatic formatting"
                                color: "#ffffff"
                                font.pixelSize: 20
                                font.weight: Font.DemiBold
                            }

                            Text {
                                Layout.fillWidth: true
                                text: "For recordings longer than 15 seconds, JFlow formats the finished Scribe transcript locally with Qwen3. Shorter dictations are inserted immediately."
                                color: "#b5b5b5"
                                font.pixelSize: 14
                                wrapMode: Text.Wrap
                            }

                            Rectangle {
                                Layout.fillWidth: true
                                implicitHeight: 1
                                color: "#292929"
                            }

                            Text {
                                text: "Context is inferred locally from the active window. It supplies at most one short hint, such as AI prompt, professional message, or casual message. If JFlow is unsure, formatting stays neutral."
                                color: "#8c8c8c"
                                font.pixelSize: 13
                                wrapMode: Text.Wrap
                                Layout.fillWidth: true
                            }

                            Text {
                                text: "The model runs on your GPU when available. If it is unavailable, too slow, or returns an unsafe result, JFlow inserts the original Scribe text and records that fallback in History."
                                color: "#8c8c8c"
                                font.pixelSize: 13
                                wrapMode: Text.Wrap
                                Layout.fillWidth: true
                            }

                            Item {
                                Layout.fillHeight: true
                            }

                        }

                    }

                    Item {
                        anchors.fill: parent
                        visible: root.page === 3

                        ColumnLayout {
                            anchors.fill: parent
                            spacing: 14

                            Text {
                                text: "Formatter audit"
                                color: "#ffffff"
                                font.pixelSize: 20
                                font.weight: Font.DemiBold
                            }

                            Text {
                                text: root.usageSummary()
                                color: "#b5b5b5"
                                font.pixelSize: 13
                                wrapMode: Text.Wrap
                                Layout.fillWidth: true
                            }

                            Text {
                                text: "Usage is audio duration, not an estimated bill. It never changes providers or sends extra cloud requests."
                                color: "#777777"
                                font.pixelSize: 12
                                wrapMode: Text.Wrap
                                Layout.fillWidth: true
                            }

                            RowLayout {
                                Layout.fillWidth: true
                                Layout.fillHeight: true
                                spacing: 12

                                ListView {
                                    id: auditList

                                    Layout.preferredWidth: 250
                                    Layout.fillHeight: true
                                    clip: true
                                    spacing: 7
                                    model: root.jobs.filter(function(job) {
                                        return job.formatting && job.formatting.eligible;
                                    })

                                    Text {
                                        anchors.centerIn: parent
                                        visible: auditList.count === 0
                                        text: "No formatter audits yet"
                                        color: "#777777"
                                        font.pixelSize: 13
                                    }

                                    delegate: Rectangle {
                                        required property var modelData

                                        width: auditList.width
                                        implicitHeight: 56
                                        radius: 9
                                        color: root.auditJobID === modelData.id ? "#ffffff" : "#191919"

                                        Column {
                                            anchors.fill: parent
                                            anchors.margins: 10
                                            spacing: 3

                                            Text {
                                                text: root.timestamp(modelData.created_at)
                                                color: root.auditJobID === modelData.id ? "#090909" : "#d8d8d8"
                                                font.pixelSize: 11
                                            }

                                            Text {
                                                text: root.formattingLabel(modelData)
                                                color: root.auditJobID === modelData.id ? "#333333" : "#8fbd9b"
                                                font.pixelSize: 11
                                                elide: Text.ElideRight
                                                width: parent.width
                                            }

                                        }

                                        MouseArea {
                                            anchors.fill: parent
                                            onClicked: root.auditJobID = modelData.id
                                        }

                                    }

                                }

                                Rectangle {
                                    Layout.fillWidth: true
                                    Layout.fillHeight: true
                                    radius: 10
                                    color: "#151515"
                                    border.color: "#282828"
                                    border.width: 1

                                    Flickable {
                                        anchors.fill: parent
                                        anchors.margins: 13
                                        clip: true
                                        contentWidth: width
                                        contentHeight: auditText.implicitHeight

                                        TextEdit {
                                            id: auditText

                                            width: parent.width
                                            readOnly: true
                                            selectByMouse: true
                                            text: root.auditDetails(root.auditJob())
                                            color: "#dedede"
                                            font.pixelSize: 12
                                            wrapMode: TextEdit.Wrap
                                        }

                                    }

                                }

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

            Text {
                id: buttonLabel

                anchors.centerIn: parent
                text: parent.label
                color: parent.prominent ? "#090909" : parent.destructive ? "#ffb5af" : "#e9e9e9"
                font.pixelSize: 11
                font.weight: Font.DemiBold
            }

            MouseArea {
                id: buttonArea

                anchors.fill: parent
                hoverEnabled: true
                onClicked: parent.clicked()
            }

        }

    }

}
