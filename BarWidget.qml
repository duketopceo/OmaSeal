import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui

BarWidget {
  id: root
  moduleName: "io.github.duketopceo.oma-ring"

  property int secretCount: 0

  visible: true
  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  function open() { if (panelLoader.item) panelLoader.item.open() }
  function close() { if (panelLoader.item) panelLoader.item.close() }
  function toggle() { if (panelLoader.item) panelLoader.item.toggle() }

  function refresh() {
    if (!countProc.running) countProc.running = true
  }

  function applyCount(raw) {
    try {
      var d = JSON.parse(raw)
      root.secretCount = d.length
    } catch (e) {
      root.secretCount = -1
    }
  }

  Loader {
    id: panelLoader
    active: true
    source: Qt.resolvedUrl("Panel.qml")
    visible: false
    onLoaded: {
      if (item) {
        item.bar = root.bar
        item.anchorItem = button
        item.hostWidget = root
      }
    }
  }

  Timer {
    interval: 30000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }

  Process {
    id: countProc
    command: ["/home/lukedaduke/.local/bin/oma-ring", "list", "--json"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.applyCount(text)
    }
  }

  WidgetButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: "O" + (root.secretCount > 0 ? " " + root.secretCount : "")
    tooltipText: "Oma Ring — " + root.secretCount + " secrets"
    onPressed: function(buttonCode) {
      if (buttonCode === Qt.LeftButton) root.toggle()
    }
  }
}
