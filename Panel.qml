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
  onSearchFilterChanged: root.rebuildModel()
  property var allSecrets: []
  property string notice: ""
  property bool showLogs: false
  property int selectedIndex: 0
  property string pendingDeleteKey: ""
  property bool refreshPending: false
  property var pendingCopy: null
  property var pendingDel: null
  property bool agentStatusPending: false
  property bool saving: false
  property string getText: ""
  property bool isAdding: false
  property string agentMode: ""
  property bool agentSessionActive: false
  property string agentSessionExpires: ""
  property bool agentKeepAlive: false
  property bool fprintdAvailable: true

  Component {
    id: setProcComponent
    Process {
      stdinEnabled: true
      stderr: StdioCollector {
        waitForEnd: true
      }
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
    root.refreshAgentStatus()
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

  function secretKey(service, account) {
    return service + "\u0001" + account
  }

  function refresh() {
    root.notice = ""
    root.pendingDeleteKey = ""
    listProc.command = ["omaseal", "list", "--json"]
    if (listProc.running) {
      root.refreshPending = true
    } else {
      listProc.running = true
    }
  }

  function refreshLog() {
    logProc.command = ["omaseal", "logs", "25", "--json"]
    if (!logProc.running) logProc.running = true
  }

  function applyList(raw) {
    try {
      var d = JSON.parse(raw)
      root.allSecrets = d
      root.rebuildModel()
    } catch (e) {
      root.notice = "Failed to parse secret list"
    }
  }

  function rebuildModel() {
    var f = root.searchFilter.trim().toLowerCase()
    // Preserve selection by identity: indexes shift under filtering/reloads.
    var selKey = ""
    if (root.selectedIndex >= 0 && root.selectedIndex < secretsModel.count) {
      var cur = secretsModel.get(root.selectedIndex)
      selKey = root.secretKey(cur.service, cur.account)
    }
    var newIdx = -1
    secretsModel.clear()
    for (var i = 0; i < root.allSecrets.length; i++) {
      var it = root.allSecrets[i]
      if (f !== "") {
        var hay = ((it.service || "") + "/" + (it.account || "") + " " + (it.label || "")).toLowerCase()
        if (hay.indexOf(f) === -1) continue
      }
      var row = {
        service: it.service || "",
        account: it.account || "",
        label: it.label || ""
      }
      secretsModel.append(row)
      if (selKey !== "" && root.secretKey(row.service, row.account) === selKey) {
        newIdx = secretsModel.count - 1
      }
    }
    if (newIdx >= 0) {
      root.selectedIndex = newIdx
    } else if (root.selectedIndex >= secretsModel.count) {
      root.selectedIndex = Math.max(0, secretsModel.count - 1)
    }
  }

  function refreshAgentStatus() {
    if (agentProc.running) {
      root.agentStatusPending = true
    } else {
      agentProc.running = true
    }
  }

  function applyAgentStatus(raw) {
    try {
      var d = JSON.parse(raw)
      root.agentMode = d.mode || ""
      root.agentSessionActive = d.session_active === true
      root.agentSessionExpires = d.session_expires || ""
      root.agentKeepAlive = d.keep_alive === true
      root.fprintdAvailable = d.fprintd_available !== false
    } catch (e) {
      root.agentMode = ""
      root.agentSessionActive = false
      root.agentSessionExpires = ""
      root.agentKeepAlive = false
      root.fprintdAvailable = true
    }
  }

  function unlockAgent() {
    if (!unlockProc.running) unlockProc.running = true
  }

  function applyLogs(raw) {
    try {
      var d = JSON.parse(raw)
      logModel.clear()
      for (var i = 0; i < d.length; i++) {
        logModel.append({
          time: d[i].time || "",
          source: d[i].source || "",
          message: d[i].message || ""
        })
      }
    } catch (e) {
      logModel.clear()
    }
  }

  function saveSecret() {
    if (root.saving) return
    var service = serviceField.text.trim()
    var account = accountField.text.trim()
    var secret = secretField.text
    if (!service || !account || !secret) {
      root.notice = "Fill in service, account, and secret"
      return
    }
    root.saving = true
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
        var detail = ""
        var lines = (proc.stderr.text || "").split("\n")
        for (var i = 0; i < lines.length; i++) {
          var l = lines[i].trim()
          if (l.indexOf("error:") === 0) { detail = l; break }
        }
        root.notice = detail !== "" ? "Save failed: " + detail : "Save failed"
      }
      root.saving = false
      proc.destroy()
    })
    proc.started.connect(function() {
      proc.write(secret)
      proc.stdinEnabled = false
    })
    proc.running = true
  }

  // Delete confirmation is armed by identity, not index: a list rebuild can
  // reshuffle rows, and the confirm must still target the armed secret.
  function deleteSecret(index, service, account) {
    var key = root.secretKey(service, account)
    if (root.pendingDeleteKey !== "" && root.pendingDeleteKey !== key) {
      root.pendingDeleteKey = ""
      deleteConfirmTimer.stop()
      root.notice = "Delete confirmation cancelled"
      return
    }
    if (root.pendingDeleteKey !== key) {
      root.pendingDeleteKey = key
      deleteConfirmTimer.restart()
      root.notice = "Press delete again to confirm deletion"
      return
    }
    deleteConfirmTimer.stop()
    root.pendingDeleteKey = ""
    if (delProc.running) {
      root.pendingDel = {service: service, account: account}
      root.notice = "Delete queued"
      return
    }
    root.notice = "Deleting..."
    delProc.command = ["omaseal", "del", service, account]
    delProc.running = true
  }

  function copySecret(service, account) {
    if (getProc.running) {
      // Coalesce: run the newest request after the in-flight get exits.
      root.pendingCopy = {service: service, account: account}
      return
    }
    getProc.command = ["omaseal", "get", service, account]
    getProc.running = true
  }

  function runPendingCopy() {
    if (root.pendingCopy === null) return
    var c = root.pendingCopy
    root.pendingCopy = null
    getProc.command = ["omaseal", "get", c.service, c.account]
    getProc.running = true
  }

  function runPendingDel() {
    if (root.pendingDel === null) return
    var d = root.pendingDel
    root.pendingDel = null
    root.notice = "Deleting..."
    delProc.command = ["omaseal", "del", d.service, d.account]
    delProc.running = true
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
    onTriggered: {
      if (root.pendingDeleteKey !== "") {
        root.pendingDeleteKey = ""
        root.notice = "Delete confirmation expired"
      }
    }
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
      if (root.refreshPending) {
        root.refreshPending = false
        root.refresh()
      }
    }
  }

  Process {
    id: getProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.getText = text
    }
    onExited: function(exitCode) {
      // Capture-and-clear: the clipboard payload must not race a queued get.
      var secret = root.getText
      root.getText = ""
      // Only a successful get reaches the clipboard — a failed or empty
      // read must never clobber what the user already has.
      if (exitCode === 0 && secret !== "") {
        var proc = copyProcComponent.createObject(root)
        proc.exited.connect(function(ec) {
          root.notice = ec === 0 ? "Copied to clipboard (clears in 30s)" : "Copy failed"
          proc.destroy()
        })
        proc.started.connect(function() {
          proc.write(secret)
          proc.stdinEnabled = false
        })
        proc.running = true
      } else {
        root.notice = "Copy failed"
      }
      root.runPendingCopy()
    }
  }

  Process {
    id: delProc
    onExited: function(exitCode) {
      if (exitCode === 0) {
        root.refresh() // clears notice
        root.notice = "Deleted"
        root.statusChanged()
      } else {
        root.notice = "Delete failed"
      }
      root.runPendingDel()
    }
  }

  Process {
    id: logProc
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.applyLogs(text)
    }
  }

  Process {
    id: agentProc
    command: ["omaseal", "agent", "status", "--json"]
    stdout: StdioCollector {
      waitForEnd: true
      onStreamFinished: root.applyAgentStatus(text)
    }
    onExited: function(exitCode) {
      if (root.agentStatusPending) {
        root.agentStatusPending = false
        root.refreshAgentStatus()
      }
    }
  }

  Process {
    id: unlockProc
    command: ["omaseal", "agent", "unlock"]
    stderr: StdioCollector {}
    onExited: function(exitCode) {
      if (exitCode === 0) {
        root.refreshAgentStatus()
      } else {
        var lines = (unlockProc.stderr.text || "").split("\n")
        var detail = ""
        for (var i = 0; i < lines.length; i++) {
          var l = lines[i].trim()
          if (l.indexOf("error:") === 0) { detail = l; break }
        }
        root.notice = detail !== "" ? "Unlock failed: " + detail : "Unlock failed"
        root.refreshAgentStatus()
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

  ListModel {
    id: logModel
  }

  Component {
    id: logsComponent

    Column {
      width: parent.width
      leftPadding: Style.space(14)
      rightPadding: Style.space(14)
      topPadding: Style.space(14)
      bottomPadding: Style.space(14)
      spacing: Style.space(10)

      RowLayout {
        width: parent.width - parent.leftPadding - parent.rightPadding
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
          text: "Back"
          bordered: true
          onClicked: root.showLogs = false
        }
      }

      PanelSectionHeader {
        text: "LOGS"
        foreground: root.fg
      }

      ListView {
        width: parent.width - parent.leftPadding - parent.rightPadding
        height: Style.space(240)
        clip: true
        model: logModel

        delegate: Column {
          width: ListView.view.width
          spacing: Style.space(2)

          Row {
            spacing: Style.space(8)

            Text {
              text: model.time
              color: root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }

            Text {
              text: model.source
              color: root.accent
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }
          }

          Text {
            width: parent.width
            text: model.message
            color: root.fg
            font.family: root.fontFamily
            font.pixelSize: Style.font.bodySmall
            wrapMode: Text.WordWrap
          }

          Rectangle {
            width: parent.width
            height: Style.space(1)
            color: root.dim
            opacity: 0.2
          }
        }
      }

      RowLayout {
        width: parent.width - parent.leftPadding - parent.rightPadding
        Item { Layout.fillWidth: true }
        Button {
          text: "Refresh"
          bordered: true
          onClicked: root.refreshLog()
        }
      }
    }
  }

  KeyboardPanel {
    id: panel
    anchorItem: root.anchorItem
    owner: root.hostWidget || root
    bar: root.bar
    open: root.opened
    focusTarget: keyCatcher
    contentWidth: panel.fittedContentWidth(Style.space(380), 460)
    contentHeight: panel.fittedContentHeight(root.showLogs ? (logLoader.item ? logLoader.item.implicitHeight + Style.space(28) : Style.space(28)) : contentColumn.implicitHeight, 640)

    PanelKeyCatcher {
      id: keyCatcher
      anchors.fill: parent
      blocked: serviceField.activeFocus || accountField.activeFocus || secretField.activeFocus || searchField.activeFocus
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
        else if (t === "/" && searchField.visible) searchField.forceActiveFocus()
      }

      Column {
        id: contentColumn
        width: parent.width
        visible: !root.showLogs
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
              Keys.onEscapePressed: root.clearAddForm()
            }

            TextField {
              id: accountField
              width: parent.width
              placeholderText: "Account (e.g. default, personal)"
              foreground: root.fg
              Keys.onEscapePressed: root.clearAddForm()
            }

            TextField {
              id: secretField
              width: parent.width
              password: true
              placeholderText: "Secret payload"
              foreground: root.fg
              Keys.onReturnPressed: root.saveSecret()
              Keys.onEscapePressed: root.clearAddForm()
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

          // Agent trust status — the Keychain-style lock indicator.
          RowLayout {
            width: parent.width
            spacing: Style.space(8)
            visible: root.agentMode !== ""

            Text {
              text: "󰌆 AGENTS " + root.agentMode.toUpperCase() +
                    (root.agentKeepAlive ? " · KA" : "") +
                    (root.agentMode === "ask" && root.agentSessionActive
                      ? " · UNLOCKED" + (root.agentSessionExpires
                          ? " " + Qt.formatTime(new Date(root.agentSessionExpires), "HH:mm")
                          : "")
                      : "")
              color: (root.agentMode === "open" || root.agentSessionActive) ? root.accent : root.dim
              font.family: root.fontFamily
              font.pixelSize: Style.font.caption
            }

            Item { Layout.fillWidth: true }

            Button {
              visible: root.agentMode === "ask" && !root.agentSessionActive
              text: "Unlock"
              bordered: true
              onClicked: root.unlockAgent()
            }
          }

          // fprintd availability notice — only relevant when ask mode gates on it.
          Text {
            visible: root.agentMode === "ask" && !root.fprintdAvailable
            width: parent.width
            text: "no fingerprint reader — unlock is ungated"
            color: root.dim
            font.family: root.fontFamily
            font.pixelSize: Style.font.caption
          }

          // Search field — Keychain Access style filtering.
          TextField {
            id: searchField
            width: parent.width
            placeholderText: "Search secrets  (press /)"
            foreground: root.fg
            visible: root.allSecrets.length > 0
            Keys.onReleased: root.searchFilter = searchField.text
            Keys.onEscapePressed: searchField.focus = false
          }

          // Secret List Section Header
          PanelSectionHeader {
            text: "SECRETS (" + secretsModel.count + ")  ·  j/k nav  ·  / search  ·  enter copy  ·  x del"
            foreground: root.fg
          }
        }

        // Scrollable secrets list
        Flickable {
          id: listFlickable
          width: parent.width - contentColumn.leftPadding - contentColumn.rightPadding
          implicitHeight: Math.min(secretsCol.implicitHeight, root.isAdding ? Style.space(220) : Style.space(420))
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
              text: root.allSecrets.length > 0 ? "No matches." : "No secrets stored in keyring."
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
                    if (root.pendingDeleteKey !== "" && root.pendingDeleteKey !== root.secretKey(service, account)) {
                      root.pendingDeleteKey = ""
                      deleteConfirmTimer.stop()
                      root.notice = "Delete confirmation cancelled"
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

      }

      Loader {
        id: logLoader
        active: root.showLogs
        visible: root.showLogs
        anchors.fill: parent
        sourceComponent: logsComponent
        onLoaded: root.refreshLog()
      }
    }
  }
}
