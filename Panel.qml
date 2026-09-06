import QtQuick
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

Panel {
  id: root
  moduleName: "io.github.duketopceo.oma-ring"
  manageIpc: false

  signal statusChanged()

  property var anchorItem: null
  property var hostWidget: null
  property var settings: null
  property bool popoutSwitchClosing: false

  property string searchFilter: ""
  property string notice: ""
  property int selectedIndex: 0
  property bool isAdding: false

  readonly property color fg: root.bar ? root.bar.foreground : Color.foreground
  readonly property color dim: Qt.darker(root.fg, 1.5)
  readonly property color urgent: Color.urgent !== undefined ? Color.urgent : "#f38ba8"
  readonly property color accent: Color.accent
  readonly property string fontFamily: root.bar ? root.bar.fontFamily : Style.font.family

  function open() {
    root.controller.show()
    root.refresh()
  }

  function close() {
    root.controller.hide()
  }

  function toggle() {
    root.opened ? close() : open()
  }

  function closeForPopoutSwitch() {
    root.close()
  }

  function switchPanel(direction) {
    if (root.bar && typeof root.bar.switchPanelFrom === "function") {
      return root.bar.switchPanelFrom(root.hostWidget || root, direction)
    }
    return false
  }

  function refresh() {
    root.notice = ""
    listProc.command = ["oma-ring", "list", "--json"]
    if (!listProc.running) listProc.running = true
  }

  function applyList(raw) {
    try {
      var d = JSON.parse(raw)
      secretsModel.clear()
      for (var i = 0; i < d.length; i++) {
        secretsModel.append({
          service: d[i].service || "",
          account: d[i].account || "",
          label: d[i].label || ""
        })
      }
      if (root.selectedIndex >= secretsModel.count) {
        root.selectedIndex = Math.max(0, secretsModel.count - 1)
      }
    } catch (e) {
      root.notice = "Failed to parse secret list"
    }
  }

  function saveSecret() {
    var service = serviceField.text.trim()
    var account = accountField.text.trim()
    var secret = secretField.text
    if (!service || !account || !secret) {
      root.notice = "Fill in service, account, and secret"
      return
    }
    root.notice = "Saving..."
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
    root.isAdding = false
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
        root.notice = "Failed to list secrets"
      }
    }
  }

  Process {
    id: getProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: {
        Quickshell.execDetached(["bash", "-c", "printf %s " + Util.shellQuote(text) + " | wl-copy --sensitive --clear-after 30"])
        root.notice = "Copied to clipboard (clears in 30s)"
      }
    }
    onExited: function(exitCode) {
      if (exitCode !== 0) {
        root.notice = "Copy failed"
      }
    }
  }

  Process {
    id: delProc
    onExited: function(exitCode) {
      if (exitCode === 0) {
        root.notice = "Deleted"
        root.refresh()
        root.statusChanged()
      } else {
        root.notice = "Delete failed"
      }
    }
  }

  Process {
    id: setProc
    onExited: function(exitCode) {
      if (exitCode === 0) {
        root.notice = "Saved"
        root.clearAddForm()
        root.refresh()
        root.statusChanged()
      } else {
        root.notice = "Save failed"
      }
    }
  }

  ListModel {
    id: secretsModel
  }

  KeyboardPanel {
    id: panel
    anchorItem: root.anchorItem
    owner: root.hostWidget || root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(380), 460)
    contentHeight: panel.fittedContentHeight(contentColumn.implicitHeight, 680)

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      blocked: serviceField.activeFocus || accountField.activeFocus || secretField.activeFocus
      onCloseRequested: root.close()
      onTabRequested: function(direction) { root.switchPanel(direction) }
      onMoveRequested: function(dx, dy) {
        if (secretsModel.count === 0) return
        var next = root.selectedIndex + dy
        if (next >= 0 && next < secretsModel.count) {
          root.selectedIndex = next
        }
      }
      onActivateRequested: {
        if (secretsModel.count > 0 && root.selectedIndex < secretsModel.count) {
          var item = secretsModel.get(root.selectedIndex)
          root.copySecret(item.service, item.account)
        }
      }
      onDeleteRequested: {
        if (secretsModel.count > 0 && root.selectedIndex < secretsModel.count) {
          var item = secretsModel.get(root.selectedIndex)
          root.deleteSecret(item.service, item.account)
        }
      }
      onTextKey: function(t) {
        if (t === "r" || t === "R") root.refresh()
        else if (t === "a" || t === "A") root.isAdding = !root.isAdding
      }

      Column {
        id: contentColumn
        width: parent.width
        leftPadding: Style.space(14)
        rightPadding: Style.space(14)
        topPadding: Style.space(14)
        bottomPadding: Style.space(14)
        spacing: Style.space(10)

        // Header
        RowLayout {
          width: parent.width - contentColumn.leftPadding - contentColumn.rightPadding
          spacing: Style.space(8)

          Text {
            text: "󰌋 Oma Ring"
            color: root.fg
            font.family: root.fontFamily
            font.pixelSize: Style.font.heading
            font.bold: true
          }

          Item { Layout.fillWidth: true }

          Button {
            text: root.isAdding ? "Cancel" : "+ Add"
            bordered: true
            onClicked: root.isAdding = !root.isAdding
          }

          PanelActionButton {
            iconText: "󰑐"
            tooltipText: "Refresh secrets (r)"
            foreground: root.fg
            onClicked: root.refresh()
          }
        }

        PanelSeparator {
          foreground: root.fg
          width: parent.width - contentColumn.leftPadding - contentColumn.rightPadding
        }

        // Add Secret Form Collapsible
        Column {
          width: parent.width - contentColumn.leftPadding - contentColumn.rightPadding
          spacing: Style.space(8)
          visible: root.isAdding

          PanelSectionHeader {
            text: "STORE NEW SECRET"
            foreground: root.fg
          }

          TextField {
            id: serviceField
            width: parent.width
            placeholderText: "Service (e.g. openrouter, github)"
            foreground: root.fg
          }

          TextField {
            id: accountField
            width: parent.width
            placeholderText: "Account (e.g. default, personal)"
            foreground: root.fg
          }

          TextField {
            id: secretField
            width: parent.width
            password: true
            placeholderText: "Secret payload"
            foreground: root.fg
            Keys.onReturnPressed: root.saveSecret()
          }

          RowLayout {
            width: parent.width
            Item { Layout.fillWidth: true }
            Button {
              text: "Save Secret"
              bordered: true
              accent: root.accent
              onClicked: root.saveSecret()
            }
          }

          PanelSeparator {
            foreground: root.fg
            width: parent.width
          }
        }

        // Secret List Section
        PanelSectionHeader {
          text: "SECRETS (" + secretsModel.count + ")  ·  j/k nav  ·  enter copy  ·  x del"
          foreground: root.fg
        }

        // Empty State
        Text {
          visible: secretsModel.count === 0
          width: parent.width - contentColumn.leftPadding - contentColumn.rightPadding
          text: "No secrets stored in keyring."
          color: root.dim
          font.family: root.fontFamily
          font.pixelSize: Style.font.bodySmall
          horizontalAlignment: Text.AlignHCenter
          topPadding: Style.space(12)
          bottomPadding: Style.space(12)
        }

        // Secrets Repeater
        Repeater {
          model: secretsModel
          delegate: BorderSurface {
            required property var modelData
            required property int index
            width: contentColumn.width - contentColumn.leftPadding - contentColumn.rightPadding
            implicitHeight: Style.space(42)
            radius: Style.cornerRadius
            color: index === root.selectedIndex ? Style.selectedFillFor(root.fg, root.accent) : Style.controlFill(false, rowMouse.containsMouse, root.fg, root.accent)
            borderSpec: Border.controlSpec(index === root.selectedIndex ? "selected" : (rowMouse.containsMouse ? "hover-cursor" : "normal"), root.fg, root.accent)

            MouseArea {
              id: rowMouse
              anchors.fill: parent
              hoverEnabled: true
              onEntered: root.selectedIndex = index
              onClicked: root.copySecret(modelData.service, modelData.account)
            }

            RowLayout {
              anchors.fill: parent
              anchors.leftMargin: Style.space(10)
              anchors.rightMargin: Style.space(8)
              spacing: Style.space(8)

              Text {
                text: "󰌋"
                color: index === root.selectedIndex ? root.accent : root.dim
                font.pixelSize: Style.font.bodySmall
              }

              Column {
                Layout.fillWidth: true
                spacing: Style.space(2)

                Text {
                  width: parent.width
                  text: modelData.service + " / " + modelData.account
                  color: root.fg
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.bodySmall
                  font.bold: true
                  elide: Text.ElideRight
                }

                Text {
                  width: parent.width
                  text: modelData.label || ""
                  color: root.dim
                  font.family: root.fontFamily
                  font.pixelSize: Style.font.caption
                  elide: Text.ElideRight
                  visible: text !== ""
                }
              }

              Row {
                spacing: Style.space(4)

                PanelActionButton {
                  iconText: "󰆏"
                  tooltipText: "Copy to clipboard (sensitive)"
                  foreground: root.fg
                  onClicked: root.copySecret(modelData.service, modelData.account)
                }

                PanelActionButton {
                  iconText: "󰆴"
                  tooltipText: "Delete secret"
                  hoverColor: root.urgent
                  foreground: root.fg
                  onClicked: root.deleteSecret(modelData.service, modelData.account)
                }
              }
            }
          }
        }

        // Status Notice Banner
        Text {
          visible: root.notice !== ""
          width: parent.width - contentColumn.leftPadding - contentColumn.rightPadding
          text: root.notice
          color: root.notice.indexOf("fail") !== -1 ? root.urgent : root.accent
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          wrapMode: Text.WordWrap
        }
      }
    }
  }
}
