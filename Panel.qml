import QtQuick
import Quickshell
import qs.Ui

PanelWindow {
  id: root
  property var bar
  property var anchorItem
  property var hostWidget
  readonly property bool opened: visible

  function open() { visible = true }
  function close() { visible = false }
  function toggle() { visible = !visible }

  width: 420
  height: 600
  color: Color.background
  border.color: Color.accent
  border.width: 1

  Rectangle {
    anchors.fill: parent
    color: root.color
    radius: Style.cornerRadius

    Column {
      anchors.fill: parent
      anchors.margins: Style.space(16)
      spacing: Style.space(12)

      Text {
        text: "Oma Ring"
        color: Color.foreground
        font.family: Style.font.family
        font.pixelSize: Style.font.heading
        font.bold: true
      }

      Text {
        width: parent.width
        text: "System keyring manager for Omarchy.\n\nBuilt on gnome-keyring + libsecret.\nUse the CLI for now:"
        color: Color.foreground
        font.family: Style.font.family
        font.pixelSize: Style.font.body
        wrapMode: Text.Wrap
      }

      Text {
        width: parent.width
        text: "oma-ring set <service> <account>\noma-ring get <service> <account>\noma-ring del <service> <account>"
        color: Color.dim
        font.family: Style.font.family
        font.pixelSize: Style.font.caption
        wrapMode: Text.Wrap
      }

      Text {
        width: parent.width
        text: "Panel UI: coming in the next build."
        color: Color.dim
        font.family: Style.font.family
        font.pixelSize: Style.font.caption
      }
    }
  }
}
