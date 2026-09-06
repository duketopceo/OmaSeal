import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Ui
import qs.Commons

Panel {
  id: root
  moduleName: "io.github.duketopceo.oma-ring"
  manageIpc: false

  property var anchorItem: null
  property var hostWidget: null
  property var settings: null
  property bool popoutSwitchClosing: false

  function open() { root.controller.show(); refresh() }
  function close() { root.controller.hide() }
  function toggle() { root.visible ? close() : open() }
  function closeForPopoutSwitch() { root.close() }

  implicitWidth: 520
  implicitHeight: 700

  Rectangle {
    anchors.fill: parent
    color: Color.background
    radius: Style.cornerRadius

    ColumnLayout {
      anchors.fill: parent
      anchors.margins: Style.space(16)
      spacing: Style.space(12)

      RowLayout {
        Layout.fillWidth: true
        Text {
          text: "Oma Ring"
          color: Color.foreground
          font.family: Style.font.family
          font.pixelSize: Style.font.heading
          font.bold: true
        }
        Item { Layout.fillWidth: true }
        Button {
          text: "Refresh"
          onClicked: root.refresh()
        }
      }

      Text {
        id: notice
        Layout.fillWidth: true
        text: ""
        color: Color.accent
        font.family: Style.font.family
        font.pixelSize: Style.font.caption
        visible: text !== ""
      }

      Rectangle {
        Layout.fillWidth: true
        Layout.fillHeight: true
        color: "transparent"
        border.color: Qt.alpha(Color.foreground, 0.5)
        border.width: 1
        radius: Style.cornerRadius

        ListView {
          id: listView
          anchors.fill: parent
          anchors.margins: Style.space(8)
          spacing: Style.space(8)
          clip: true
          model: ListModel { id: secretsModel }

          delegate: Rectangle {
            width: listView.width
            height: 48
            color: Qt.lighter(Color.background, 1.15)
            radius: Style.cornerRadius

            RowLayout {
              anchors.fill: parent
              anchors.margins: Style.space(8)

              ColumnLayout {
                Layout.fillWidth: true
                Text {
                  text: service + " / " + account
                  color: Color.foreground
                  font.family: Style.font.family
                  font.pixelSize: Style.font.body
                  font.bold: true
                }
                Text {
                  text: label || ""
                  color: Qt.alpha(Color.foreground, 0.5)
                  font.family: Style.font.family
                  font.pixelSize: Style.font.caption
                }
              }

              Row {
                spacing: Style.space(8)
                Button {
                  text: "Copy"
                  onClicked: root.copySecret(service, account)
                }
                Button {
                  text: "Delete"
                  onClicked: root.deleteSecret(service, account)
                }
              }
            }
          }

          Text {
            anchors.centerIn: parent
            text: "No secrets stored."
            color: Qt.alpha(Color.foreground, 0.5)
            font.family: Style.font.family
            font.pixelSize: Style.font.body
            visible: secretsModel.count === 0
          }
        }
      }

      ColumnLayout {
        Layout.fillWidth: true
        spacing: Style.space(8)

        Text {
          text: "Add secret"
          color: Color.foreground
          font.family: Style.font.family
          font.pixelSize: Style.font.body
          font.bold: true
        }

        TextField {
          id: serviceField
          Layout.fillWidth: true
          placeholderText: "service (e.g. openrouter)"
          color: Color.foreground
        }

        TextField {
          id: accountField
          Layout.fillWidth: true
          placeholderText: "account (e.g. default)"
          color: Color.foreground
        }

        TextField {
          id: secretField
          Layout.fillWidth: true
          placeholderText: "secret"
          echoMode: TextInput.Password
          color: Color.foreground
        }

        Button {
          text: "Save"
          Layout.alignment: Qt.AlignRight
          onClicked: root.saveSecret()
        }
      }
    }
  }

  function refresh() {
    notice.text = ""
    listProc.command = ["oma-ring", "list", "--json"]
    if (!listProc.running) listProc.running = true
  }

  function applyList(raw) {
    try {
      var d = JSON.parse(raw)
      secretsModel.clear()
      for (var i = 0; i < d.length; i++) {
        secretsModel.append({
          service: d[i].service,
          account: d[i].account,
          label: d[i].label
        })
      }
    } catch (e) {
      notice.text = "Failed to load list"
    }
  }

  function saveSecret() {
    var service = serviceField.text
    var account = accountField.text
    var secret = secretField.text
    if (!service || !account || !secret) {
      notice.text = "Fill in all fields"
      return
    }
    notice.text = "Saving..."
    var cmd = "printf %s " + Util.shellQuote(secret) + " | oma-ring set " + Util.shellQuote(service) + " " + Util.shellQuote(account)
    setProc.command = ["bash", "-c", cmd]
    if (!setProc.running) setProc.running = true
  }

  function deleteSecret(service, account) {
    delProc.command = ["oma-ring", "del", service, account]
    if (!delProc.running) delProc.running = true
  }

  function copySecret(service, account) {
    getProc.command = ["oma-ring", "get", service, account]
    if (!getProc.running) getProc.running = true
  }

  function clearAddForm() {
    serviceField.text = ""
    accountField.text = ""
    secretField.text = ""
  }

  Process {
    id: listProc
    command: ["oma-ring", "list", "--json"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.applyList(text)
    }
    onExited: function(exitCode) {
      if (exitCode !== 0) {
        notice.text = "List failed"
      }
    }
  }

  Process {
    id: getProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        Quickshell.execDetached(["bash", "-c", "printf %s " + Util.shellQuote(text) + " | wl-copy"])
        notice.text = "Copied to clipboard"
      }
    }
    onExited: function(exitCode) {
      if (exitCode !== 0) {
        notice.text = "Copy failed"
      }
    }
  }

  Process {
    id: delProc
    onExited: function(exitCode) {
      if (exitCode === 0) {
        notice.text = "Deleted"
        root.refresh()
      } else {
        notice.text = "Delete failed"
      }
    }
  }

  Process {
    id: setProc
    onExited: function(exitCode) {
      if (exitCode === 0) {
        notice.text = "Saved"
        root.clearAddForm()
        root.refresh()
      } else {
        notice.text = "Save failed"
      }
    }
  }

  onVisibleChanged: {
    if (visible) root.refresh()
  }
}
