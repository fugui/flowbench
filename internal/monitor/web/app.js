(function () {
  "use strict";

  var currentSession = null;
  var sessionTimerInterval = null;
  var sessionStartEpoch = 0;
  var transactionTraces = [];
  var capturedFrames = [];
  var recordedMocks = [];
  var selectedTrace = null;
  var selectedFrame = null;
  var activeFramingSpec = null;
  var activeFilter = "";

  function el(tag, className, text) {
    var node = document.createElement(tag);
    if (className) {
      node.className = className;
    }
    if (text !== undefined && text !== null) {
      node.textContent = String(text);
    }
    return node;
  }

  function setText(id, text) {
    var node = document.getElementById(id);
    if (node) {
      node.textContent = String(text);
    }
  }

  function inRange(idx, off, width) {
    return idx >= off && idx < off + width;
  }

  function dynamicByteMeta(idx, spec) {
    if (spec && spec.preset_id === "cros-rpc-v1") {
      if (idx <= 6) return { cls: "byte-cell byte-magic", label: "Magic" };
      if (idx === 7) return { cls: "byte-cell byte-op", label: "底座" };
      if (idx >= 8 && idx <= 15) return { cls: "byte-cell byte-reqid", label: "SNO" };
      if (idx >= 16 && idx <= 19) return { cls: "byte-cell byte-len", label: "总长" };
      if (idx >= 20 && idx <= 21) return { cls: "byte-cell byte-crc", label: "头长" };
      if (idx === 22) return { cls: "byte-cell byte-op", label: "Type" };
      if (idx === 23) return { cls: "byte-cell byte-magic", label: "Pack" };
      return { cls: "byte-cell", label: "会话" };
    }
    if (!spec) {
      if (idx <= 1) return { cls: "byte-cell byte-magic", label: "Magic" };
      if (idx === 3) return { cls: "byte-cell byte-op", label: "Op" };
      if (idx >= 8 && idx <= 15) return { cls: "byte-cell byte-reqid", label: "SeqID" };
      if (idx >= 16 && idx <= 19) return { cls: "byte-cell byte-len", label: "Len" };
      if (idx >= 20 && idx <= 23) return { cls: "byte-cell byte-crc", label: "CRC32" };
      return { cls: "byte-cell", label: "Hdr" };
    }
    if (spec.magic && spec.magic.enabled && inRange(idx, spec.magic.offset, spec.magic.length)) {
      return { cls: "byte-cell byte-magic", label: "Magic" };
    }
    if (spec.opcode_field && inRange(idx, spec.opcode_field.offset, spec.opcode_field.width)) {
      return { cls: "byte-cell byte-op", label: "Op" };
    }
    if (spec.seq_id_field && inRange(idx, spec.seq_id_field.offset, spec.seq_id_field.width)) {
      return { cls: "byte-cell byte-reqid", label: "SeqID" };
    }
    if (spec.length_field && inRange(idx, spec.length_field.offset, spec.length_field.width)) {
      return { cls: "byte-cell byte-len", label: "Len" };
    }
    if (spec.checksum_field && spec.checksum_field.enabled && inRange(idx, spec.checksum_field.offset, spec.checksum_field.width)) {
      return { cls: "byte-cell byte-crc", label: "Chk" };
    }
    return { cls: "byte-cell", label: "Hdr" };
  }

  // 会话生命周期与计时器
  function updateSessionUI(sess) {
    if (!sess) return;
    currentSession = sess;

    var statusText = "待命就绪 (IDLE)";
    var dotCls = "status-dot dot-idle";
    var isRec = sess.status === "RECORDING";

    var btnStart = document.getElementById("btn-session-start");
    var btnStop = document.getElementById("btn-session-stop");
    var timerEl = document.getElementById("session-timer");

    if (isRec) {
      statusText = "🔴 录制中 (RECORDING)";
      dotCls = "status-dot dot-recording";
      if (btnStart) btnStart.style.display = "none";
      if (btnStop) btnStop.style.display = "inline-block";
      if (timerEl) timerEl.style.display = "inline-block";
      startSessionTimer();
    } else {
      if (sess.status === "STOPPED") {
        statusText = "⏹ 会话已固化 (STOPPED)";
        dotCls = "status-dot dot-stopped";
      }
      if (btnStart) btnStart.style.display = "inline-block";
      if (btnStop) btnStop.style.display = "none";
      stopSessionTimer();
    }

    setText("session-status-text", statusText);
    var dot = document.getElementById("session-dot");
    if (dot) dot.className = dotCls;

    var pidStr = sess.target_pid > 0
      ? "PID " + sess.target_pid + " (" + sess.process_name + ":" + sess.ingress_port + ")"
      : "Sandbox:9100";
    setText("top-target-label", pidStr);
    setText("top-session-count", (sess.total_traces || 0) + " 事务 / " + (sess.total_frames || 0) + " 帧");

    setText("topo-sut-sub", pidStr);
    setText("link-ingress-label", "connect4(:" + sess.ingress_port + ")");

    if (sess.traces && sess.traces.length > 0) {
      transactionTraces = sess.traces;
      renderTracesList(transactionTraces);
    }
  }

  function startSessionTimer() {
    if (sessionTimerInterval) return;
    sessionStartEpoch = Date.now();
    sessionTimerInterval = setInterval(function () {
      var diffSec = Math.floor((Date.now() - sessionStartEpoch) / 1000);
      var h = Math.floor(diffSec / 3600);
      var m = Math.floor((diffSec % 3600) / 60);
      var s = diffSec % 60;
      var str = (h < 10 ? "0" + h : h) + ":" + (m < 10 ? "0" + m : m) + ":" + (s < 10 ? "0" + s : s);
      setText("session-timer", str);
    }, 1000);
  }

  function stopSessionTimer() {
    if (sessionTimerInterval) {
      clearInterval(sessionTimerInterval);
      sessionTimerInterval = null;
    }
  }

  function startRecordingSession() {
    var pidVal = parseInt(document.getElementById("input-target-pid").value || "0", 10);
    var ingPort = parseInt(document.getElementById("input-ingress-port").value || "9100", 10);

    fetch("/api/session/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        title: "手工联调录制 (" + new Date().toLocaleTimeString() + ")",
        target_pid: pidVal,
        process_name: "target-proc-" + pidVal,
        ingress_port: ingPort
      })
    })
      .then(function (r) {
        return r.json();
      })
      .then(function (res) {
        if (res.session) {
          updateSessionUI(res.session);
          var cfgDrawer = document.getElementById("config-drawer");
          if (cfgDrawer) cfgDrawer.style.display = "none";
        }
      })
      .catch(function (err) {
        alert("开启监控录制失败: " + String(err));
      });
  }

  function stopRecordingSession() {
    fetch("/api/session/stop", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({})
    })
      .then(function (r) {
        return r.json();
      })
      .then(function (res) {
        if (res.session) {
          updateSessionUI(res.session);
          loadSessionTraces();
        }
      })
      .catch(function (err) {
        alert("停止监控录制失败: " + String(err));
      });
  }

  function loadSessionTraces() {
    fetch("/api/session/traces")
      .then(function (r) {
        return r.json();
      })
      .then(function (res) {
        transactionTraces = res.traces || [];
        renderTracesList(transactionTraces);
      })
      .catch(function () {});
  }

  // 渲染端到端调用事务树
  function renderTracesList(traces) {
    var container = document.getElementById("trace-items-list");
    var emptyHint = document.getElementById("trace-empty-hint");
    if (!container) return;

    var list = traces || [];
    setText("trace-count-label", "共 " + list.length + " 笔调用");

    if (list.length === 0) {
      if (emptyHint) emptyHint.style.display = "block";
      return;
    }
    if (emptyHint) emptyHint.style.display = "none";

    container.replaceChildren();

    for (var i = list.length - 1; i >= 0; i--) {
      (function (tr) {
        var card = el("div", "trace-card " + (selectedTrace && selectedTrace.trace_id === tr.trace_id ? "active-trace" : ""));

        // 头部
        var head = el("div", "trace-head");
        var hLeft = el("div", "th-left");
        hLeft.appendChild(el("span", "th-reqid", "#" + tr.request_id));
        hLeft.appendChild(el("span", "th-op", tr.op_name));

        var statusBadgeCls = tr.status_name === "StatusOK" || tr.status_code === 200 || tr.status_code === 0x12
          ? "badge badge-pass"
          : "badge badge-fail";
        hLeft.appendChild(el("span", statusBadgeCls, tr.status_name || "DONE"));
        head.appendChild(hLeft);

        var hRight = el("div", "th-right");
        hRight.appendChild(el("span", "th-lat", (tr.duration_ms || 0).toFixed(1) + "ms"));
        head.appendChild(hRight);
        card.appendChild(head);

        // 子调用树
        var subBox = el("div", "trace-sub-items");

        // Ingress 请求简要
        var inReqRow = el("div", "sub-row");
        inReqRow.appendChild(el("span", "sub-tag", "⬆️ Ingress"));
        var inReqTxt = tr.ingress_req && tr.ingress_req.protobuf_json
          ? JSON.stringify(tr.ingress_req.protobuf_json).slice(0, 45) + "..."
          : tr.ingress_req.header.op_name;
        inReqRow.appendChild(el("span", "sub-op", inReqTxt));
        subBox.appendChild(inReqRow);

        // 各个 Egress 子调用
        var subCalls = tr.sub_calls || [];
        for (var s = 0; s < subCalls.length; s++) {
          var sc = subCalls[s];
          var scRow = el("div", "sub-row");
          scRow.appendChild(el("span", "sub-tag", "➡️ " + (sc.backend_name || sc.target_backend || "Egress")));
          scRow.appendChild(el("span", "sub-op", sc.op_name));
          scRow.appendChild(el("span", "sub-lat", (sc.duration_ms || 0).toFixed(1) + "ms"));
          subBox.appendChild(scRow);
        }

        // Ingress 响应简要
        if (tr.ingress_resp) {
          var inRespRow = el("div", "sub-row");
          inRespRow.appendChild(el("span", "sub-tag", "⬇️ 响应"));
          var inRespTxt = tr.ingress_resp.protobuf_json
            ? JSON.stringify(tr.ingress_resp.protobuf_json).slice(0, 45) + "..."
            : "CRC32=" + tr.ingress_resp.header.crc32_hex;
          inRespRow.appendChild(el("span", "sub-op", inRespTxt));
          subBox.appendChild(inRespRow);
        }

        card.appendChild(subBox);

        // 操作栏
        var actRow = el("div", "trace-actions-row");
        var btnInspect = el("button", "btn btn-xs btn-outline", "🔬 解剖");
        btnInspect.type = "button";
        btnInspect.addEventListener("click", function (e) {
          e.stopPropagation();
          selectTraceForInspect(tr);
        });
        actRow.appendChild(btnInspect);

        var btnReplay = el("button", "btn btn-xs btn-primary", "🚀 原地重放");
        btnReplay.type = "button";
        btnReplay.addEventListener("click", function (e) {
          e.stopPropagation();
          selectTraceForReplay(tr);
        });
        actRow.appendChild(btnReplay);

        card.appendChild(actRow);

        card.addEventListener("click", function () {
          selectTraceForInspect(tr);
        });

        container.appendChild(card);
      })(list[i]);
    }
  }

  function selectTraceForInspect(tr) {
    selectedTrace = tr;
    switchTab("panel-inspector");
    if (tr.ingress_req) {
      renderFrameDetail(tr.ingress_req);
    } else if (tr.ingress_resp) {
      renderFrameDetail(tr.ingress_resp);
    }
    renderTracesList(transactionTraces);
  }

  function selectTraceForReplay(tr) {
    selectedTrace = tr;
    switchTab("panel-replayer");

    setText("replay-op-tag", "#" + tr.request_id + " " + tr.op_name);
    setText(
      "replay-meta-text",
      "录制耗时: " + (tr.duration_ms || 0).toFixed(2) + "ms | 状态: " + tr.status_name + " | 子调用: " + (tr.sub_calls ? tr.sub_calls.length : 0) + " 个"
    );

    var ta = document.getElementById("textarea-replay-payload");
    if (ta && tr.ingress_req && tr.ingress_req.protobuf_json) {
      ta.value = JSON.stringify(tr.ingress_req.protobuf_json, null, 2);
    }

    var resBox = document.getElementById("replay-result-box");
    if (resBox) resBox.style.display = "none";

    renderTracesList(transactionTraces);
  }

  function triggerSingleReplay() {
    if (!selectedTrace) {
      alert("请先从左侧选择一条调用事务！");
      return;
    }

    var ta = document.getElementById("textarea-replay-payload");
    var overrideJSON = null;
    if (ta && ta.value.trim()) {
      try {
        overrideJSON = JSON.parse(ta.value.trim());
      } catch (err) {
        alert("请求载荷 JSON 格式错误: " + String(err));
        return;
      }
    }

    var btn = document.getElementById("btn-trigger-single-replay");
    if (btn) {
      btn.disabled = true;
      btn.textContent = "🚀 正在重放执行中...";
    }

    fetch("/api/replay/single", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        trace_id: selectedTrace.trace_id,
        override_payload_json: overrideJSON,
        mock_egress: true
      })
    })
      .then(function (r) {
        return r.json();
      })
      .then(function (res) {
        renderReplayResult(res);
      })
      .catch(function (err) {
        alert("单调用重放执行失败: " + String(err));
      })
      .finally(function () {
        if (btn) {
          btn.disabled = false;
          btn.textContent = "🚀 立即原地自愈重放 (Replay Single Call)";
        }
      });
  }

  function renderReplayResult(res) {
    var resBox = document.getElementById("replay-result-box");
    if (!resBox) return;
    resBox.style.display = "block";

    var badge = document.getElementById("replay-pass-badge");
    if (badge) {
      badge.className = "badge " + (res.passed ? "badge-pass" : "badge-fail");
      badge.textContent = res.passed ? "✅ PASS 校验一致" : "⚠️ DIFF 发现差异";
    }

    setText(
      "replay-summary-text",
      "CRC32: 预期 " + res.expected_crc32 + " vs 实际 " + res.actual_crc32 + " | 重放耗时: " + (res.latency_ms || 0).toFixed(2) + "ms"
    );

    // 差异展示
    var diffBox = document.getElementById("replay-diff-box");
    var diffList = document.getElementById("replay-diff-list");
    if (diffBox && diffList) {
      if (res.field_diffs && res.field_diffs.length > 0) {
        diffBox.style.display = "block";
        diffList.replaceChildren();
        for (var i = 0; i < res.field_diffs.length; i++) {
          var d = res.field_diffs[i];
          diffList.appendChild(
            el("div", "diff-item", "字段 [" + d.field + "]: 预期 [" + d.expected + "] -> 实际 [" + d.actual + "]")
          );
        }
      } else {
        diffBox.style.display = "none";
      }
    }

    // Side-by-Side 响应展示
    if (selectedTrace && selectedTrace.ingress_resp) {
      setText("replay-expected-json", JSON.stringify(selectedTrace.ingress_resp.protobuf_json || {}, null, 2));
    }
    if (res.actual_response) {
      setText("replay-actual-json", JSON.stringify(res.actual_response.protobuf_json || {}, null, 2));
    }
  }

  function switchTab(targetPanelId) {
    var panels = document.querySelectorAll(".tab-panel");
    for (var i = 0; i < panels.length; i++) {
      panels[i].classList.remove("active");
    }
    var target = document.getElementById(targetPanelId);
    if (target) target.classList.add("active");

    var btns = document.querySelectorAll(".tab-btn");
    for (var j = 0; j < btns.length; j++) {
      btns[j].classList.remove("active");
    }
    if (targetPanelId === "panel-inspector") {
      document.getElementById("tab-btn-inspector").classList.add("active");
    } else if (targetPanelId === "panel-replayer") {
      document.getElementById("tab-btn-replayer").classList.add("active");
    } else if (targetPanelId === "panel-mock") {
      document.getElementById("tab-btn-mock").classList.add("active");
    }
  }

  // 遥测与动态拓扑更新
  function renderTelemetry(telemetry) {
    if (!telemetry) return;

    var qps = (telemetry.total_qps || 0).toFixed(1);
    var kbps = (telemetry.total_kbps || 0).toFixed(1);
    setText("top-telemetry-throughput", kbps + " KB/s (" + qps + " QPS)");

    var links = telemetry.links || {};

    // Ingress 链路
    var ing = links["client->sut"];
    var ingLink = document.getElementById("link-ingress");
    if (ingLink && ing) {
      setText("link-ingress-label", "connect4(:9100) | " + ing.qps.toFixed(1) + " QPS");
      if (ing.active) {
        ingLink.classList.add("flow-active");
      } else {
        ingLink.classList.remove("flow-active");
      }
    }

    // Egress 链路
    var eg = links["sut->egress"];
    var egLink = document.getElementById("link-egress");
    if (egLink && eg) {
      setText("link-egress-label", "connect4(:9200) | " + eg.qps.toFixed(1) + " QPS");
      if (eg.active) {
        egLink.classList.add("flow-active");
      } else {
        egLink.classList.remove("flow-active");
      }
    }
  }

  function renderFrameDetail(frame) {
    if (!frame) return;
    selectedFrame = frame;

    setText(
      "selected-frame-title",
      "#" +
        frame.seq_id +
        " | " +
        frame.direction +
        " | " +
        frame.header.op_name +
        " (ReqID=" +
        frame.header.request_id +
        ")"
    );

    var hexArr = frame.header_bytes_hex || [];
    setText(
      "byte-matrix-caption",
      (frame.header.protocol_name || "自定义二进制协议") +
        " — 帧头字节色彩解剖图 (共 " +
        hexArr.length +
        " 字节)"
    );

    var matrix = document.getElementById("header-byte-matrix");
    if (matrix) {
      matrix.replaceChildren();
      for (var i = 0; i < hexArr.length; i++) {
        var meta = dynamicByteMeta(i, activeFramingSpec);
        var cell = el("div", meta.cls);
        var offLabel = el("span", "b-off", (i < 10 ? "0" + i : String(i)) + ":" + meta.label);
        var hexVal = el("span", "b-hex", hexArr[i] || "00");
        cell.appendChild(offLabel);
        cell.appendChild(hexVal);
        matrix.appendChild(cell);
      }
    }

    var kvGrid = document.getElementById("header-kv-grid");
    if (kvGrid) {
      kvGrid.replaceChildren();
      var items = [
        ["Protocol / Magic", (frame.header.protocol_name || "Custom") + " (" + (frame.header.magic_hex || "None") + ")"],
        ["OpCode", frame.header.op_code_hex + " (" + frame.header.op_name + ")"],
        ["RequestID / SNO", String(frame.header.request_id)],
        ["PayloadLen", frame.header.payload_length + " bytes"],
        ["Checksum", frame.header.crc32_hex + (frame.crc32_valid ? " [VALID]" : " [INVALID]")],
        ["Schema 反射状态", frame.header.schema_decoded ? "dynamicpb 明文" : "Wire Tag 降级树"]
      ];
      if (frame.header.service_name || frame.header.operation) {
        items.splice(1, 0, [
          "CROS 契约",
          (frame.header.service_name || "") + "." + (frame.header.operation || "")
        ]);
      }
      for (var j = 0; j < items.length; j++) {
        var box = el("div", "kv-box");
        box.appendChild(el("div", "k", items[j][0]));
        box.appendChild(el("div", "v", items[j][1]));
        kvGrid.appendChild(box);
      }
    }

    setText("detail-protobuf-json", JSON.stringify(frame.protobuf_json || {}, null, 2));
    setText(
      "detail-raw-hex",
      "[Wire Hex (" +
        (frame.raw_wire_hex ? frame.raw_wire_hex.length / 2 : 0) +
        "B)]:\n" +
        (frame.raw_wire_hex || "") +
        "\n\n[Base64]:\n" +
        (frame.raw_base64 || "")
    );
  }

  function renderProcessList(procs) {
    var tbody = document.getElementById("proc-list-tbody");
    if (!tbody) return;
    tbody.replaceChildren();

    var list = procs || [];
    for (var i = 0; i < list.length; i++) {
      (function (p) {
        var tr = el("tr", "");
        tr.appendChild(el("td", "", String(p.pid)));
        tr.appendChild(el("td", "", p.name));
        var listenStr = p.listen_ports && p.listen_ports.length > 0 ? p.listen_ports.join(", ") : "-";
        tr.appendChild(el("td", "", listenStr));

        var tdAct = el("td", "");
        var btn = el("button", "btn btn-xs btn-primary", "选定");
        btn.type = "button";
        btn.addEventListener("click", function (ev) {
          ev.stopPropagation();
          var pidIn = document.getElementById("input-target-pid");
          var ingIn = document.getElementById("input-ingress-port");
          if (pidIn) pidIn.value = String(p.pid);
          if (ingIn && p.listen_ports && p.listen_ports.length > 0) {
            ingIn.value = String(p.listen_ports[0]);
          }
          attachSelectedProcess(p.pid, p.name);
        });
        tdAct.appendChild(btn);
        tr.appendChild(tdAct);
        tbody.appendChild(tr);
      })(list[i]);
    }
  }

  function scanProcesses() {
    var qInput = document.getElementById("proc-search-input");
    var q = qInput ? qInput.value : "";
    fetch("/api/processes?q=" + encodeURIComponent(q))
      .then(function (r) {
        return r.json();
      })
      .then(function (data) {
        renderProcessList(data.processes || []);
      })
      .catch(function () {});
  }

  function attachSelectedProcess(pidOverride, procName) {
    var pidVal = pidOverride !== undefined
      ? pidOverride
      : parseInt(document.getElementById("input-target-pid").value || "0", 10);
    var ingPort = parseInt(document.getElementById("input-ingress-port").value || "9100", 10);
    var egIP = document.getElementById("input-egress-ip").value || "127.0.0.1";
    var egPort = parseInt(document.getElementById("input-egress-port").value || "9200", 10);

    fetch("/api/attach", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        target_pid: pidVal,
        process_name: procName || "user-proc-" + pidVal,
        ingress_port: ingPort,
        egress_ip: egIP,
        egress_port: egPort
      })
    })
      .then(function (r) {
        return r.json();
      })
      .then(function () {
        loadInitialState();
      })
      .catch(function () {});
  }

  function detachProcess() {
    fetch("/api/detach", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({})
    })
      .then(function () {
        document.getElementById("input-target-pid").value = "0";
        loadInitialState();
      })
      .catch(function () {});
  }

  function renderBackendChips(backends) {
    var container = document.getElementById("backend-chips-container");
    if (!container) return;
    container.replaceChildren();
    if (!backends || backends.length === 0) {
      container.appendChild(el("span", "sub-hint", "未注册多后端 (默认连向 127.0.0.1:9200)"));
      return;
    }
    for (var i = 0; i < backends.length; i++) {
      (function (b) {
        var chip = el("span", "backend-chip");
        chip.appendChild(el("span", "chip-name", b.name + ": " + b.host + ":" + b.port));
        var delBtn = el("span", "chip-del", "×");
        delBtn.title = "注销此 Backend";
        delBtn.addEventListener("click", function () {
          removeBackend(b.host, b.port);
        });
        chip.appendChild(delBtn);
        container.appendChild(chip);
      })(backends[i]);
    }
  }

  function addBackend() {
    var nameInput = document.getElementById("input-new-backend-name");
    var hostInput = document.getElementById("input-new-backend-host");
    var portInput = document.getElementById("input-new-backend-port");
    if (!portInput || !portInput.value) return;

    var nameVal = nameInput ? nameInput.value.trim() : "";
    var hostVal = hostInput ? hostInput.value.trim() || "127.0.0.1" : "127.0.0.1";
    var portVal = parseInt(portInput.value, 10);
    if (!portVal) return;

    fetch("/api/backends/add", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: nameVal || "backend-" + hostVal + "-" + portVal,
        host: hostVal,
        port: portVal,
        active: true
      })
    })
      .then(function () {
        if (portInput) portInput.value = "";
        loadInitialState();
      })
      .catch(function () {});
  }

  function removeBackend(host, port) {
    fetch("/api/backends/remove", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ host: host, port: port })
    })
      .then(function () {
        loadInitialState();
      })
      .catch(function () {});
  }

  function initSSE() {
    var es = new EventSource("/api/events");
    es.onopen = function () {};
    es.onmessage = function (ev) {
      try {
        var payload = JSON.parse(ev.data);
        if (payload.event_type === "telemetry") {
          renderTelemetry(payload.telemetry);
        } else {
          // 帧事件到达，刷新事务列表并展示最新帧
          capturedFrames.push(payload);
          loadSessionTraces();
        }
      } catch (ignore) {}
    };
  }

  function loadInitialState() {
    fetch("/api/status")
      .then(function (r) {
        return r.json();
      })
      .then(function (data) {
        if (data.current_session) {
          updateSessionUI(data.current_session);
        }
        if (data.telemetry) {
          renderTelemetry(data.telemetry);
        }
        if (data.attached_target) {
          renderBackendChips(data.attached_target.backends || []);
        }
        if (data.framing_spec) {
          activeFramingSpec = data.framing_spec;
          setText("topo-sut-metric", "协议: " + (data.framing_spec.preset_id || data.framing_spec.protocol_name));
        }
        if (data.ebpf_status) {
          setText("top-ebpf-mode", data.ebpf_status.mode || "eBPF Active");
        }
        loadSessionTraces();
      })
      .catch(function () {});
  }

  document.addEventListener("DOMContentLoaded", function () {
    // 会话主控按钮
    document.getElementById("btn-session-start").addEventListener("click", startRecordingSession);
    document.getElementById("btn-session-stop").addEventListener("click", stopRecordingSession);

    // 折叠抽屉切换
    document.getElementById("btn-toggle-config").addEventListener("click", function () {
      var d = document.getElementById("config-drawer");
      if (d) d.style.display = d.style.display === "none" ? "block" : "none";
    });
    document.getElementById("btn-close-config").addEventListener("click", function () {
      document.getElementById("config-drawer").style.display = "none";
    });

    document.getElementById("btn-toggle-sandbox").addEventListener("click", function () {
      var d = document.getElementById("sandbox-drawer");
      if (d) d.style.display = d.style.display === "none" ? "block" : "none";
    });
    document.getElementById("btn-close-sandbox").addEventListener("click", function () {
      document.getElementById("sandbox-drawer").style.display = "none";
    });

    // Tab 切换
    document.getElementById("tab-btn-inspector").addEventListener("click", function () {
      switchTab("panel-inspector");
    });
    document.getElementById("tab-btn-replayer").addEventListener("click", function () {
      switchTab("panel-replayer");
    });
    document.getElementById("tab-btn-mock").addEventListener("click", function () {
      switchTab("panel-mock");
    });

    // 单调用重放
    document.getElementById("btn-trigger-single-replay").addEventListener("click", triggerSingleReplay);

    // 进程扫描
    document.getElementById("btn-refresh-procs").addEventListener("click", scanProcesses);
    document.getElementById("proc-search-input").addEventListener("input", scanProcesses);
    document.getElementById("btn-attach-custom").addEventListener("click", function () {
      attachSelectedProcess();
    });
    document.getElementById("btn-detach-proc").addEventListener("click", detachProcess);

    // 后端管理
    var addBackendBtn = document.getElementById("btn-add-backend");
    if (addBackendBtn) addBackendBtn.addEventListener("click", addBackend);

    // 事务过滤
    var filterInput = document.getElementById("trace-filter-input");
    if (filterInput) {
      filterInput.addEventListener("input", function () {
        var q = filterInput.value.trim().toLowerCase();
        if (!q) {
          renderTracesList(transactionTraces);
          return;
        }
        var filtered = transactionTraces.filter(function (tr) {
          return (
            tr.op_name.toLowerCase().indexOf(q) !== -1 ||
            String(tr.request_id).indexOf(q) !== -1 ||
            (tr.status_name && tr.status_name.toLowerCase().indexOf(q) !== -1)
          );
        });
        renderTracesList(filtered);
      });
    }

    document.getElementById("btn-refresh-traces").addEventListener("click", loadSessionTraces);

    loadInitialState();
    scanProcesses();
    initSSE();
  });
})();
