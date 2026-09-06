import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui
import qs.Commons

BarWidget {
  id: root
  moduleName: "io.github.duketopceo.oma-ring"

  property int secretCount: 0

  readonly property var panelItem: panelLoader.item
  readonly property bool opened: panelItem ? panelItem.opened === true : false
  readonly property bool popoutSwitchClosing: panelItem
    ? panelItem.popoutSwitchClosing === true
    : false

  function open() { if (panelItem) panelItem.open() }
  function close() { if (panelItem) panelItem.close() }
  function toggle() { if (panelItem) panelItem.toggle() }
  function closeForPopoutSwitch() { if (panelItem) panelItem.closeForPopoutSwitch() }

  function refresh() {
    if (!countProc.running) countProc.running = true
  }

  function applyCount(raw) {
    try {
      var d = JSON.parse(raw)
      root.secretCount = d.length
    } catch (e) {
      root.secretCount = 0
    }
  }

  function injectPanel() {
    if (!panelItem) return
    if ("bar" in panelItem) panelItem.bar = root.bar
    if ("settings" in panelItem) panelItem.settings = root.settings
    if ("anchorItem" in panelItem) panelItem.anchorItem = button
    if ("hostWidget" in panelItem) panelItem.hostWidget = root
  }

  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  onBarChanged: injectPanel()
  onSettingsChanged: injectPanel()

  Loader {
    id: panelLoader
    active: true
    source: Qt.resolvedUrl("Panel.qml")
    visible: false
    onLoaded: {
      root.injectPanel()
      Qt.callLater(root.injectPanel)
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
    command: ["oma-ring", "list", "--json"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.applyCount(text)
    }
  }

  Connections {
    target: panelLoader.item
    ignoreUnknownSignals: true
    function onStatusChanged() { root.refresh() }
  }

  WidgetButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: "󰌋" + (root.secretCount > 0 ? " " + root.secretCount : "")
    tooltipText: "Oma Ring — " + root.secretCount + " secrets"
    onPressed: function(buttonCode) {
      if (buttonCode === Qt.LeftButton) root.toggle()
    }
  }
}
