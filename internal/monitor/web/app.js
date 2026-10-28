(function () {
  "use strict";

  var currentSession = null;
  var sessionTimerInterval = null;
  var sessionStartEpoch = 0;
  var transactionTraces = [];
  var capturedFrames = [];
  var recordedMocks = [];
  var selectedTrace = null;
  var selectedStep = null;
  var selectedFrame = null;
  var activeFramingSpec = null;
  var activeFilter = "";
  var isProtocolDrawerOpen = false;

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

  // 渲染业务请求调用用例列表 (Business Calls)
  function renderTracesList(traces) {
    var container = document.getElementById("trace-items-list");
    var emptyHint = document.getElementById("trace-empty-hint");
    if (!container) return;

    var list = traces || [];
    setText("trace-count-label", "共 " + list.length + " 笔用例");

    if (list.length === 0) {
      if (emptyHint) emptyHint.style.display = "block";
      return;
    }
    if (emptyHint) emptyHint.style.display = "none";

    container.replaceChildren();

    for (var i = list.length - 1; i >= 0; i--) {
      (function (tr) {
        var isSelected = selectedTrace && selectedTrace.trace_id === tr.trace_id;
        var card = el("div", "trace-card " + (isSelected ? "active-trace" : ""));

        // 头部
        var head = el("div", "trace-head");
        var hLeft = el("div", "th-left");
        hLeft.appendChild(el("span", "th-reqid", "#" + tr.request_id));
        hLeft.appendChild(el("span", "th-op", tr.op_name));

        var isOk = tr.status_name === "StatusOK" || tr.status_code === 200 || tr.status_code === 0x12;
        var statusBadgeCls = isOk ? "badge badge-pass" : "badge badge-fail";
        hLeft.appendChild(el("span", statusBadgeCls, tr.status_name || "DONE"));
        head.appendChild(hLeft);

        var hRight = el("div", "th-right");
        hRight.appendChild(el("span", "th-lat", (tr.duration_ms || 0).toFixed(1) + "ms"));
        head.appendChild(hRight);
        card.appendChild(head);

        // 业务概要摘要
        var subBox = el("div", "trace-sub-items");

        // 1. 入站请求摘要
        var inRow = el("div", "sub-row");
        inRow.appendChild(el("span", "sub-tag", "💻 入站调用"));
        var inTxt = tr.ingress_req && tr.ingress_req.protobuf_json
          ? JSON.stringify(tr.ingress_req.protobuf_json).slice(0, 42) + "..."
          : tr.ingress_req ? tr.ingress_req.header.op_name : tr.op_name;
        inRow.appendChild(el("span", "sub-op", inTxt));
        subBox.appendChild(inRow);

        // 2. 下游交互统计 (直观展示下游微服务别名或组件)
        var subCalls = tr.sub_calls || [];
        var backRow = el("div", "sub-row");
        backRow.appendChild(el("span", "sub-tag", "🗄️ 下游交互"));
        if (subCalls.length > 0) {
          var backNames = [];
          for (var s = 0; s < subCalls.length; s++) {
            var bName = subCalls[s].backend_name || subCalls[s].target_backend || "下游依赖";
            if (backNames.indexOf(bName) === -1) backNames.push(bName);
          }
          backRow.appendChild(el("span", "sub-op", subCalls.length + " 次 (" + backNames.join(", ") + ")"));
        } else {
          backRow.appendChild(el("span", "sub-op", "无下游依赖 (短路本地处理)"));
        }
        subBox.appendChild(backRow);

        // 3. 重放回归标记
        if (tr.last_replay) {
          var repRow = el("div", "sub-row");
          repRow.appendChild(el("span", "sub-tag", "🚀 重放状态"));
          if (tr.last_replay.passed) {
            repRow.appendChild(el("span", "badge badge-pass", "✅ 回归一致"));
          } else {
            var diffCnt = tr.last_replay.field_diffs ? tr.last_replay.field_diffs.length : 1;
            repRow.appendChild(el("span", "badge badge-fail", "⚠️ 差异 " + diffCnt + " 处"));
          }
          subBox.appendChild(repRow);
        }

        card.appendChild(subBox);

        // 操作按钮行
        var actRow = el("div", "trace-actions-row");
        var btnUML = el("button", "btn btn-xs btn-outline", "📊 业务时序");
        btnUML.type = "button";
        btnUML.addEventListener("click", function (e) {
          e.stopPropagation();
          selectTrace(tr);
        });
        actRow.appendChild(btnUML);

        var btnReplay = el("button", "btn btn-xs btn-primary", "🚀 原地重放");
        btnReplay.type = "button";
        btnReplay.addEventListener("click", function (e) {
          e.stopPropagation();
          selectTrace(tr);
          executeReplayForTrace(null);
        });
        actRow.appendChild(btnReplay);

        card.appendChild(actRow);

        card.addEventListener("click", function () {
          selectTrace(tr);
        });

        container.appendChild(card);
      })(list[i]);
    }
  }

  function selectTrace(tr) {
    selectedTrace = tr;

    setText("current-trace-title", "用例 #" + tr.request_id + " " + tr.op_name);
    setText(
      "current-trace-meta",
      "总耗时: " +
        (tr.duration_ms || 0).toFixed(2) +
        "ms | 状态: " +
        tr.status_name +
        " | 下游交互: " +
        (tr.sub_calls ? tr.sub_calls.length : 0) +
        " 次"
    );

    var qBtn = document.getElementById("btn-quick-replay-trace");
    if (qBtn) qBtn.style.display = "inline-block";

    var umlBox = document.getElementById("uml-sequence-box");
    if (umlBox) umlBox.style.display = "block";

    var bizBox = document.getElementById("business-payload-box");
    if (bizBox) bizBox.style.display = "block";

    var protoBox = document.getElementById("protocol-drawer-box");
    if (protoBox) protoBox.style.display = "block";

    var emptyHint = document.getElementById("empty-workbench-hint");
    if (emptyHint) emptyHint.style.display = "none";

    // 渲染重放比对结果
    if (tr.last_replay) {
      renderReplayResult(tr.last_replay);
    } else {
      var resBox = document.getElementById("replay-result-box");
      if (resBox) resBox.style.display = "none";
    }

    // 渲染 UML 时序图
    var umlData = tr.uml_diagram || buildFallbackUML(tr);
    renderUMLDiagram(umlData);

    // 默认高亮选择第 1 步 (入站请求)
    if (umlData.steps && umlData.steps.length > 0) {
      selectUMLStep(umlData.steps[0]);
    }

    renderTracesList(transactionTraces);
  }

  function buildFallbackUML(tr) {
    var participants = [
      { id: "caller", display_name: "业务调用端 (Caller)", role: "CALLER" },
      { id: "sut", display_name: "目标服务 (SUT:9100)", role: "SUT" }
    ];
    var subCalls = tr.sub_calls || [];
    for (var i = 0; i < subCalls.length; i++) {
      var bName = subCalls[i].backend_name || subCalls[i].target_backend || "127.0.0.1:9200";
      participants.push({
        id: "backend-" + i,
        display_name: bName,
        role: "BACKEND"
      });
    }

    var steps = [];
    var stepIdx = 1;
    steps.push({
      step_index: stepIdx++,
      from_id: "caller",
      to_id: "sut",
      from_name: "业务调用端 (Caller)",
      to_name: "目标服务 (SUT)",
      is_return: false,
      op_name: tr.op_name,
      duration_ms: 0,
      frame_event: tr.ingress_req
    });

    for (var j = 0; j < subCalls.length; j++) {
      var sc = subCalls[j];
      var backID = "backend-" + j;
      var backName = sc.backend_name || sc.target_backend || "下游组件";
      steps.push({
        step_index: stepIdx++,
        from_id: "sut",
        to_id: backID,
        from_name: "目标服务 (SUT)",
        to_name: backName,
        is_return: false,
        op_name: sc.op_name,
        duration_ms: 0,
        frame_event: sc.egress_req
      });
      if (sc.egress_resp) {
        steps.push({
          step_index: stepIdx++,
          from_id: backID,
          to_id: "sut",
          from_name: backName,
          to_name: "目标服务 (SUT)",
          is_return: true,
          is_mockable: true,
          mock_asset_id: "session-mock-" + sc.sub_call_id,
          op_name: sc.egress_resp.header.op_name,
          duration_ms: sc.duration_ms,
          frame_event: sc.egress_resp
        });
      }
    }

    if (tr.ingress_resp) {
      steps.push({
        step_index: stepIdx++,
        from_id: "sut",
        to_id: "caller",
        from_name: "目标服务 (SUT)",
        to_name: "业务调用端 (Caller)",
        is_return: true,
        op_name: tr.ingress_resp.header.op_name,
        duration_ms: tr.duration_ms,
        frame_event: tr.ingress_resp
      });
    }

    return {
      trace_id: tr.trace_id,
      request_id: tr.request_id,
      total_duration_ms: tr.duration_ms,
      participants: participants,
      steps: steps
    };
  }

  function renderUMLDiagram(umlData) {
    var container = document.getElementById("uml-canvas-container");
    if (!container) return;
    container.replaceChildren();

    var participants = umlData.participants || [];
    var steps = umlData.steps || [];

    // 1. 生命线参与者头部
    var pHeader = el("div", "uml-participants-header");
    pHeader.style.gridTemplateColumns = "repeat(" + Math.max(participants.length, 2) + ", minmax(130px, 1fr))";

    for (var p = 0; p < participants.length; p++) {
      var pt = participants[p];
      var roleCls = "role-backend";
      if (pt.role === "CALLER") roleCls = "role-caller";
      else if (pt.role === "SUT") roleCls = "role-sut";

      var pBox = el("div", "uml-participant-box " + roleCls, pt.display_name);
      pBox.title = pt.endpoint || pt.display_name;
      pHeader.appendChild(pBox);
    }
    container.appendChild(pHeader);

    // 2. 步骤消息流 (交互式连线)
    var stepsFlow = el("div", "uml-steps-flow");

    for (var s = 0; s < steps.length; s++) {
      (function (st) {
        var isAct = selectedStep && selectedStep.step_index === st.step_index;
        var row = el("div", "uml-step-row " + (isAct ? "active-step" : ""));
        row.id = "uml-step-row-" + st.step_index;

        // 序号圆圈
        var numBadge = el("span", "uml-step-num", String(st.step_index));
        row.appendChild(numBadge);

        // 内容
        var content = el("div", "uml-step-content");

        // 端点标签: From ──▶ To
        var endp = el("div", "uml-step-endpoints");
        endp.appendChild(el("span", "uml-step-from", st.from_name));

        var arrow = el("span", "uml-arrow-indicator " + (st.is_return ? "is-return" : ""), st.is_return ? " ◀┄┄ " : " ──▶ ");
        endp.appendChild(arrow);

        endp.appendChild(el("span", "uml-step-to", st.to_name));
        content.appendChild(endp);

        // 消息名称
        var opTag = el("span", "uml-step-op", st.op_name);
        content.appendChild(opTag);

        // 耗时与 Mock 标签
        var meta = el("div", "uml-step-meta");
        if (st.duration_ms > 0) {
          meta.appendChild(el("span", "sub-lat", st.duration_ms.toFixed(2) + "ms"));
        }
        if (st.is_mockable) {
          meta.appendChild(el("span", "mock-badge-tag", "🎭 可Mock"));
        }
        content.appendChild(meta);

        row.appendChild(content);

        row.addEventListener("click", function () {
          selectUMLStep(st);
        });

        stepsFlow.appendChild(row);
      })(steps[s]);
    }

    container.appendChild(stepsFlow);
  }

  function selectUMLStep(st) {
    selectedStep = st;

    // 更新 active-step 样式
    var allRows = document.querySelectorAll(".uml-step-row");
    for (var i = 0; i < allRows.length; i++) {
      allRows[i].classList.remove("active-step");
    }
    var curRow = document.getElementById("uml-step-row-" + st.step_index);
    if (curRow) curRow.classList.add("active-step");

    // 更新业务面板头部
    setText("bpc-step-num", "步骤 " + st.step_index);
    setText("bpc-step-title", st.from_name + (st.is_return ? " ◀┄┄ " : " ──▶ ") + st.to_name + " : " + st.op_name);

    // 载入业务 JSON
    var ta = document.getElementById("textarea-step-payload");
    if (ta) {
      var pbJSON = st.frame_event && st.frame_event.protobuf_json ? st.frame_event.protobuf_json : {};
      ta.value = JSON.stringify(pbJSON, null, 2);
    }

    // 动态调整操作按钮与提示词
    var btnReplay = document.getElementById("btn-step-replay");
    var btnMock = document.getElementById("btn-step-mock");

    if (st.step_index === 1 || st.from_id === "caller") {
      if (btnReplay) btnReplay.style.display = "inline-block";
      if (btnMock) btnMock.style.display = "none";
      setText("bpc-hint-text", "💡 当前为入站请求：可直接修改下方业务 JSON 字段（如数量、用户ID），点击【🚀 用此入参重放测试 SUT】验证单组件分支逻辑。");
    } else if (st.is_mockable) {
      if (btnReplay) btnReplay.style.display = "none";
      if (btnMock) btnMock.style.display = "inline-block";
      setText("bpc-hint-text", "💡 当前为下游返回：可直接修改下方返回字段（如信用分、库存），保存后立即作为离线 Mock 挡板重放，验证 SUT 异常降级分支。");
    } else {
      if (btnReplay) btnReplay.style.display = "inline-block";
      if (btnMock) btnMock.style.display = "none";
      setText("bpc-hint-text", "💡 当前为内部步骤：展示 dynamicpb 反序列化业务明文字段。");
    }

    // 联动更新底层帧头排障抽屉
    if (st.frame_event) {
      renderFrameDetail(st.frame_event);
      var crcOk = st.frame_event.crc32_valid;
      var drawerToggle = document.getElementById("btn-toggle-protocol-drawer");
      if (drawerToggle) {
        if (!crcOk) {
          drawerToggle.classList.add("has-error");
          setText("protocol-drawer-status-text", "⚠️ 步骤 " + st.step_index + " 底层帧头校验异常 (CRC32 不匹配) | 点击排障");
          // 异常时自动展开抽屉
          var drawerContent = document.getElementById("protocol-drawer-content");
          if (drawerContent) {
            drawerContent.style.display = "block";
            isProtocolDrawerOpen = true;
            setText("protocol-drawer-arrow", "▲ 收起");
          }
        } else {
          drawerToggle.classList.remove("has-error");
          setText("protocol-drawer-status-text", "🔧 底层协议帧头与二进制 Hex (校验正常 ✓ | 点击展开排障)");
        }
      }
    }
  }

  function executeReplayForTrace(overrideJSON) {
    if (!selectedTrace) {
      alert("请先从左侧选择一条业务调用用例！");
      return;
    }

    var btnReplay = document.getElementById("btn-step-replay");
    var btnQuick = document.getElementById("btn-quick-replay-trace");
    if (btnReplay) { btnReplay.disabled = true; btnReplay.textContent = "🚀 正在重放执行中..."; }
    if (btnQuick) { btnQuick.disabled = true; btnQuick.textContent = "🚀 正在重放执行中..."; }

    fetch("/api/replay/single", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        trace_id: selectedTrace.trace_id,
        override_payload_json: overrideJSON,
        mock_egress: true
      })
    })
      .then(function (r) { return r.json(); })
      .then(function (res) {
        renderReplayResult(res);
        selectedTrace.last_replay = res;
        for (var i = 0; i < transactionTraces.length; i++) {
          if (transactionTraces[i].trace_id === selectedTrace.trace_id) {
            transactionTraces[i].last_replay = res;
            break;
          }
        }
        renderTracesList(transactionTraces);
      })
      .catch(function (err) {
        alert("单调用重放执行失败: " + String(err));
      })
      .finally(function () {
        if (btnReplay) { btnReplay.disabled = false; btnReplay.textContent = "🚀 用此入参重放测试 SUT"; }
        if (btnQuick) { btnQuick.disabled = false; btnQuick.textContent = "🚀 原地重放此用例"; }
      });
  }

  function handleSaveMockAndReplay() {
    if (!selectedStep || !selectedStep.is_mockable) return;
    var ta = document.getElementById("textarea-step-payload");
    if (!ta || !ta.value.trim()) return;

    var mockJSON = null;
    try {
      mockJSON = JSON.parse(ta.value.trim());
    } catch (err) {
      alert("下游 Mock 响应 JSON 语法错误: " + String(err));
      return;
    }

    var mockID = selectedStep.mock_asset_id;
    if (!mockID) {
      alert("未找到该步骤关联的 MockID");
      return;
    }

    var btnMock = document.getElementById("btn-step-mock");
    if (btnMock) { btnMock.disabled = true; btnMock.textContent = "💾 正在保存 Mock 并触发重放..."; }

    fetch("/api/mocks/update", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        mock_id: mockID,
        response_json: mockJSON
      })
    })
      .then(function (r) { return r.json(); })
      .then(function () {
        // 保存成功后立即触发单调用重放验证
        executeReplayForTrace(null);
      })
      .catch(function (err) {
        alert("保存下游 Mock 挡板失败: " + String(err));
      })
      .finally(function () {
        if (btnMock) { btnMock.disabled = false; btnMock.textContent = "💾 保存并作为离线 Mock 挡板重放"; }
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

    // 原地重放与 Mock 挡板事件绑定
    var btnStepReplay = document.getElementById("btn-step-replay");
    if (btnStepReplay) {
      btnStepReplay.addEventListener("click", function () {
        var ta = document.getElementById("textarea-step-payload");
        var ov = null;
        if (ta && ta.value.trim() && selectedStep && selectedStep.step_index === 1) {
          try {
            ov = JSON.parse(ta.value.trim());
          } catch (e) {
            alert("请求 JSON 语法错误: " + String(e));
            return;
          }
        }
        executeReplayForTrace(ov);
      });
    }

    var btnQuickReplay = document.getElementById("btn-quick-replay-trace");
    if (btnQuickReplay) {
      btnQuickReplay.addEventListener("click", function () {
        executeReplayForTrace(null);
      });
    }

    var btnStepMock = document.getElementById("btn-step-mock");
    if (btnStepMock) {
      btnStepMock.addEventListener("click", handleSaveMockAndReplay);
    }

    // 底层协议抽屉切换
    var drawerToggle = document.getElementById("btn-toggle-protocol-drawer");
    if (drawerToggle) {
      drawerToggle.addEventListener("click", function () {
        isProtocolDrawerOpen = !isProtocolDrawerOpen;
        var content = document.getElementById("protocol-drawer-content");
        if (content) {
          content.style.display = isProtocolDrawerOpen ? "block" : "none";
        }
        setText("protocol-drawer-arrow", isProtocolDrawerOpen ? "▲ 收起" : "▼ 展开");
      });
    }

    // 原始帧平铺流水切换
    var btnRawFrames = document.getElementById("btn-view-raw-frames");
    var isRawFramesOpen = false;
    if (btnRawFrames) {
      btnRawFrames.addEventListener("click", function () {
        isRawFramesOpen = !isRawFramesOpen;
        var rfView = document.getElementById("raw-frames-view");
        var trView = document.getElementById("trace-items-list");
        if (rfView) rfView.style.display = isRawFramesOpen ? "block" : "none";
        if (trView) trView.style.display = isRawFramesOpen ? "none" : "block";
        btnRawFrames.textContent = isRawFramesOpen ? "📡 业务调用用例" : "📋 原始帧流水";
      });
    }

    // 兼容历史按钮 (若存在)
    var tabInsp = document.getElementById("tab-btn-inspector");
    if (tabInsp) tabInsp.addEventListener("click", function () { switchTab("panel-inspector"); });
    var tabRepl = document.getElementById("tab-btn-replayer");
    if (tabRepl) tabRepl.addEventListener("click", function () { switchTab("panel-replayer"); });
    var tabMock = document.getElementById("tab-btn-mock");
    if (tabMock) tabMock.addEventListener("click", function () { switchTab("panel-mock"); });
    var btnLegacyReplay = document.getElementById("btn-trigger-single-replay");
    if (btnLegacyReplay) btnLegacyReplay.addEventListener("click", function () { executeReplayForTrace(null); });

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
