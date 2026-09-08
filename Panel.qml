import QtQuick
import QtQuick.Layouts
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui

Panel {
  id: root
  moduleName: "io.github.duketopceo.omaseal"
  manageIpc: false

  signal statusChanged()

  property var anchorItem: null
  property var hostWidget: null
  property var settings: null
  property bool popoutSwitchClosing: false

  property string searchFilter: ""
  property string notice: ""
  property string logText: ""
  property bool showLogs: false
  property int selectedIndex: 0
  property bool isAdding: false
  property int pendingDeleteIndex: -1

  Component {
    id: setProcComponent
    Process {
      stdinEnabled: true
    }
  }

  Component {
    id: copyProcComponent
    Process {
      stdinEnabled: true
      command: ["wl-copy", "--sensitive", "--clear-after", "30"]
    }
  }

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
    root.pendingDeleteIndex = -1
    listProc.command = ["omaseal", "list", "--json"]
    if (!listProc.running) listProc.running = true
  }

  function refreshLog() {
    logProc.command = ["omaseal", "logs", "25"]
    if (!logProc.running) logProc.running = true
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
    var proc = setProcComponent.createObject(root)
    proc.command = ["omaseal", "set", service, account]
    proc.exited.connect(function(exitCode) {
      if (exitCode === 0) {
        root.notice = "Saved"
        root.clearAddForm()
        root.refresh()
        root.statusChanged()
      } else {
        root.notice = "Save failed"
      }
      proc.destroy()
    })
    proc.running = true
    proc.write(secret)
    proc.stdinEnabled = false
  }

  function deleteSecret(index, service, account) {
    if (root.pendingDeleteIndex !== -1 && root.pendingDeleteIndex !== index) {
      root.pendingDeleteIndex = -1
      deleteConfirmTimer.stop()
      root.notice = "Delete confirmation cancelled"
      return
    }
    if (root.pendingDeleteIndex !== index) {
      root.pendingDeleteIndex = index
      deleteConfirmTimer.restart()
      root.notice = "Press delete again to confirm deletion"
      return
    }
    deleteConfirmTimer.stop()
    root.pendingDeleteIndex = -1
    root.notice = "Deleting..."
    delProc.command = ["omaseal", "del", service, account]
    if (!delProc.running) delProc.running = true
  }

  function copySecret(service, account) {
    getProc.command = ["omaseal", "get", service, account]
    if (!getProc.running) getProc.running = true
  }

  function clearAddForm() {
    serviceField.text = ""
    accountField.text = ""
    secretField.text = ""
    root.isAdding = false
  }

  Timer {
    id: deleteConfirmTimer
    interval: 5000
    onTriggered: root.pendingDeleteIndex = -1
  }

  Process {
    id: listProc
    command: ["omaseal", "list", "--json"]
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
        var proc = copyProcComponent.createObject(root)
        proc.exited.connect(function(exitCode) {
          if (exitCode === 0) {
            root.notice = "Copied to clipboard (clears in 30s)"
          } else {
            root.notice = "Copy failed"
          }
          proc.destroy()
        })
        proc.running = true
        proc.write(text)
        proc.stdinEnabled = false
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
    id: logProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.logText = String(text || "").trim()
    }
    onExited: function(exitCode) {
      if (exitCode !== 0) {
        root.logText = ""
      }
    }
  }

  Timer {
    id: logTimer
    interval: 3000
    repeat: true
    running: root.showLogs && root.opened
    onTriggered: root.refreshLog()
  }

  onShowLogsChanged: {
    if (root.showLogs) root.refreshLog()
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
    contentHeight: panel.fittedContentHeight(headerCol.implicitHeight + listFlickable.height + (root.notice !== "" ? noticeText.implicitHeight + Style.space(10) : 0) + (root.showLogs ? logView.implicitHeight + Style.space(10) : 0) + Style.space(28), 640)

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
          root.deleteSecret(root.selectedIndex, item.service, item.account)
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

        // Static header area
        Column {
          id: headerCol
          width: parent.width - contentColumn.leftPadding - contentColumn.rightPadding
          spacing: Style.space(10)

          RowLayout {
            width: parent.width
            spacing: Style.space(8)

            Text {
              text: "󰌋 OmaSeal"
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

            Button {
              text: "Logs"
              bordered: true
              onClicked: root.showLogs = !root.showLogs
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
            width: parent.width
          }

          // Add Secret Form Collapsible
          Column {
            width: parent.width
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

          // Secret List Section Header
          PanelSectionHeader {
            text: "SECRETS (" + secretsModel.count + ")  ·  j/k nav  ·  enter copy  ·  x del"
            foreground: root.fg
          }
        }

        // Scrollable secrets list
        Flickable {
          id: listFlickable
          width: parent.width - contentColumn.leftPadding - contentColumn.rightPadding
          implicitHeight: Math.min(secretsCol.implicitHeight, Style.space(420))
          height: implicitHeight
          contentHeight: secretsCol.implicitHeight
          clip: true

          Column {
            id: secretsCol
            width: parent.width
            spacing: Style.space(6)

            // Empty State
            Text {
              visible: secretsModel.count === 0
              width: parent.width
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
                required property int index
                required property string service
                required property string account
                required property string label
                width: secretsCol.width
                implicitHeight: Style.space(42)
                radius: Style.cornerRadius
                color: index === root.selectedIndex ? Style.selectedFillFor(root.fg, root.accent) : Style.controlFill(false, rowMouse.containsMouse, root.fg, root.accent)
                borderSpec: Border.controlSpec(index === root.selectedIndex ? "selected" : (rowMouse.containsMouse ? "hover-cursor" : "normal"), root.fg, root.accent)

                MouseArea {
                  id: rowMouse
                  anchors.fill: parent
                  hoverEnabled: true
                  onEntered: {
                    if (root.pendingDeleteIndex !== -1 && root.pendingDeleteIndex !== index) {
                      root.pendingDeleteIndex = -1
                      deleteConfirmTimer.stop()
                    }
                    root.selectedIndex = index
                  }
                  onClicked: root.copySecret(service, account)
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
                      text: service + " / " + account
                      color: root.fg
                      font.family: root.fontFamily
                      font.pixelSize: Style.font.bodySmall
                      font.bold: true
                      elide: Text.ElideRight
                    }

                    Text {
                      width: parent.width
                      text: label || ""
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
                      onClicked: root.copySecret(service, account)
                    }

                    PanelActionButton {
                      iconText: "󰆴"
                      tooltipText: "Delete secret"
                      hoverColor: root.urgent
                      foreground: root.fg
                      onClicked: root.deleteSecret(index, service, account)
                    }
                  }
                }
              }
            }
          }
        }

        // Status Notice Banner
        Text {
          id: noticeText
          visible: root.notice !== ""
          width: parent.width - contentColumn.leftPadding - contentColumn.rightPadding
          text: root.notice
          color: root.notice.indexOf("fail") !== -1 ? root.urgent : root.accent
          font.family: root.fontFamily
          font.pixelSize: Style.font.caption
          wrapMode: Text.WordWrap
        }

        // Click-to-open Log View
        Column {
          id: logView
          visible: root.showLogs
          width: parent.width - contentColumn.leftPadding - contentColumn.rightPadding
          spacing: Style.space(6)

          PanelSectionHeader {
            text: "LOGS"
            foreground: root.fg
          }

          Flickable {
            width: parent.width
            height: Style.space(180)
            contentWidth: parent.width
            contentHeight: logDisplay.implicitHeight
            clip: true

            Text {
              id: logDisplay
              width: parent.width
              text: root.logText
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
              wrapMode: Text.WordWrap
              opacity: 0.85
            }
          }

          RowLayout {
            width: parent.width
            Item { Layout.fillWidth: true }
            Button {
              text: "Hide Logs"
              bordered: true
              onClicked: root.showLogs = false
            }
          }
        }
      }
    }
  }
}
