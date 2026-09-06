import QtQuick
import Quickshell
import qs.Ui

BarWidget {
  id: root
  moduleName: "io.github.duketopceo.oma-ring"

  property bool available: true
  property int secretCount: 0

  visible: available
  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  function open() {
    if (panelLoader.item) panelLoader.item.open()
  }

  function toggle() {
    if (panelLoader.item) panelLoader.item.toggle()
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

  WidgetButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: "O"
    tooltipText: "Oma Ring — system keyring"
    onPressed: function(buttonCode) {
      if (buttonCode === Qt.LeftButton) root.toggle()
    }
  }
}
