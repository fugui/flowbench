(function () {
  "use strict";

  var capturedFrames = [];
  var recordedMocks = [];
  var selectedSeqID = null;
  var activeFramingSpec = null;

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

  function renderEBPFMetrics(status, attachedTarget, framingSpec) {
    if (status) {
      setText("metric-mode", status.mode || "UNKNOWN");
      setText("metric-prog-id", status.program_id ? "#" + status.program_id : "Userspace");
      setText(
        "metric-intercept-cnt",
        (status.stat_ingress_redirected || 0) + " / " + (status.stat_egress_redirected || 0)
      );
    }
    if (attachedTarget) {
      var pidLabel = attachedTarget.target_pid > 0
        ? "PID " + attachedTarget.target_pid + " (" + attachedTarget.process_name + ")"
        : "Sandbox (PID=0)";
      setText("metric-target-pid", pidLabel);
      setText(
        "attach-status-badge",
        attachedTarget.attached
          ? "🎯 已挂载: PID " + attachedTarget.target_pid + " | 入站:" + attachedTarget.ingress_orig_port + " | 下游:" + attachedTarget.egress_orig_ip + ":" + attachedTarget.egress_orig_port
          : "当前：内置沙箱演练模式"
      );
      setText(
        "topo-server-desc",
        "127.0.0.1:" + attachedTarget.ingress_orig_port + " (TargetPID=" + attachedTarget.target_pid + ")"
      );
      setText(
        "topo-egress-arrow",
        "connect4(" + attachedTarget.egress_orig_ip + ":" + attachedTarget.egress_orig_port + ")"
      );
      renderBackendChips(attachedTarget.backends || []);
    }
    if (framingSpec) {
      activeFramingSpec = framingSpec;
      setText("metric-proto-name", framingSpec.preset_id || framingSpec.protocol_name);
      setText(
        "topo-wire-tag",
        framingSpec.min_header_bytes + "B (" + framingSpec.byte_order + ") + Protobuf"
      );
      setText(
        "framing-spec-summary",
        "帧头: " +
          framingSpec.min_header_bytes +
          "B | 字节序: " +
          framingSpec.byte_order +
          " | 长度: Offset " +
          framingSpec.length_field.offset +
          " (" +
          framingSpec.length_field.semantics +
          ") | SeqID: Offset " +
          framingSpec.seq_id_field.offset +
          " (" +
          framingSpec.seq_id_field.width +
          "B) | Checksum: " +
          framingSpec.checksum_field.algorithm +
          " (" +
          framingSpec.checksum_field.scope +
          ")"
      );
    }
  }

  function renderSchemaStatus(schemaStatus) {
    if (!schemaStatus) return;
    setText(
      "schema-status-badge",
      schemaStatus.schema_loaded
        ? "✅ 已加载: " + schemaStatus.package_name + " (" + schemaStatus.total_messages + " Messages)"
        : "⚠️ 无 Schema 模式 (protowire Tag 树降级激活)"
    );
    var ta = document.getElementById("textarea-proto-source");
    if (ta && !ta.value && schemaStatus.proto_source) {
      ta.value = schemaStatus.proto_source;
    }
  }

  function renderMockSelector(mocks) {
    recordedMocks = mocks || [];
    var sel = document.getElementById("select-mock-asset");
    if (!sel) return;
    var prevVal = sel.value;
    sel.replaceChildren();

    if (recordedMocks.length === 0) {
      sel.appendChild(el("option", "", "-- 先运行全链路录制以生成出站二进制 Mock 资产 --"));
      return;
    }

    for (var i = 0; i < recordedMocks.length; i++) {
      var m = recordedMocks[i];
      var opt = el(
        "option",
        "",
        m.mock_id + " | " + m.op_name + " (CRC32=" + m.original_resp.header.crc32_hex + ", Hits=" + m.hit_count + ")"
      );
      opt.value = m.mock_id;
      sel.appendChild(opt);
    }

    if (prevVal) {
      sel.value = prevVal;
    }
    if (!sel.value && recordedMocks.length > 0) {
      sel.value = recordedMocks[0].mock_id;
    }
    syncMockEditorTextarea();
  }

  function syncMockEditorTextarea() {
    var sel = document.getElementById("select-mock-asset");
    var ta = document.getElementById("textarea-mock-json");
    if (!sel || !ta) return;
    for (var i = 0; i < recordedMocks.length; i++) {
      if (recordedMocks[i].mock_id === sel.value) {
        ta.value = JSON.stringify(recordedMocks[i].original_resp.protobuf_json || {}, null, 2);
        return;
      }
    }
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
        var epStr = p.active_endpoints && p.active_endpoints.length > 0 ? p.active_endpoints.slice(0, 2).join(", ") : "-";
        tr.appendChild(el("td", "", listenStr));
        tr.appendChild(el("td", "", epStr));

        var tdAct = el("td", "");
        var btn = el("button", "btn btn-sm btn-primary", "🎯 选定");
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
      .then(function (res) {
        renderEBPFMetrics(res.ebpf_status, res.attached_target, activeFramingSpec);
      })
      .catch(function () {});
  }

  function detachProcess() {
    fetch("/api/detach", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({})
    })
      .then(function (r) {
        return r.json();
      })
      .then(function (res) {
        document.getElementById("input-target-pid").value = "0";
        renderEBPFMetrics(res.ebpf_status, res.attached_target, activeFramingSpec);
      })
      .catch(function () {});
  }

  function renderBackendChips(backends) {
    var container = document.getElementById("backend-chips-container");
    if (!container) return;
    container.replaceChildren();
    if (!backends || backends.length === 0) {
      container.appendChild(el("span", "sub-hint", "未注册多后端（默认使用主下游配置）"));
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
      .then(function (r) {
        return r.json();
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
      body: JSON.stringify({
        host: host,
        port: port
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

  function applyFramingPreset() {
    var sel = document.getElementById("select-framing-preset");
    if (!sel) return;
    fetch("/api/protocol/spec", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        preset_id: sel.value,
        simulate: true,
        sim_op_code: 0x11,
        sim_seq_id: 88001,
        sim_json: {
          userId: "USR-DSL-TEST",
          skuId: "SKU-PRO-01",
          quantity: 3,
          vipLevel: 2,
          region: "CN-EAST"
        }
      })
    })
      .then(function (r) {
        return r.json();
      })
      .then(function (res) {
        renderEBPFMetrics(null, null, res.framing_spec);
        if (res.simulated_frame) {
          renderFrameDetail(res.simulated_frame);
        }
      })
      .catch(function () {});
  }

  function updateProtoSchema(action) {
    var ta = document.getElementById("textarea-proto-source");
    var src = ta ? ta.value : "";
    fetch("/api/protocol/schema", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        action: action,
        file_name: "custom_service.proto",
        proto_source: src
      })
    })
      .then(function (r) {
        return r.json();
      })
      .then(function (res) {
        if (res.schema_status) {
          renderSchemaStatus(res.schema_status);
          if (action === "restore_default" && ta && res.schema_status.proto_source) {
            ta.value = res.schema_status.proto_source;
          }
        }
        loadInitialState();
      })
      .catch(function () {});
  }

  function saveEditedMockJSON() {
    var sel = document.getElementById("select-mock-asset");
    var ta = document.getElementById("textarea-mock-json");
    if (!sel || !ta || !sel.value) return;

    var parsed;
    try {
      parsed = JSON.parse(ta.value);
    } catch (err) {
      setText("mock-edit-status", "❌ JSON 语法错误: " + String(err));
      return;
    }

    fetch("/api/mocks/update", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        mock_id: sel.value,
        response_json: parsed
      })
    })
      .then(function (r) {
        if (!r.ok) {
          throw new Error("HTTP " + r.status);
        }
        return r.json();
      })
      .then(function (res) {
        if (res.updated_mock) {
          setText(
            "mock-edit-status",
            "✅ 已重编码二进制帧并自愈 Checksum: " + res.updated_mock.original_resp.header.crc32_hex
          );
          renderFrameDetail(res.updated_mock.original_resp);
          loadInitialState();
        }
      })
      .catch(function (err) {
        setText("mock-edit-status", "❌ 更新失败: " + String(err));
      });
  }

  function renderFrameDetail(frame) {
    if (!frame) return;
    selectedSeqID = frame.seq_id;
    setText(
      "selected-frame-title",
      "#" +
        frame.seq_id +
        " | " +
        frame.direction +
        " | " +
        frame.header.op_name +
        " (SeqID=" +
        frame.header.request_id +
        ")"
    );

    var hexArr = frame.header_bytes_hex || [];
    setText(
      "byte-matrix-caption",
      (frame.header.protocol_name || "自定义二进制协议") +
        " — 帧头字节色块解剖图 (共 " +
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
        ["SeqID / RequestID", String(frame.header.request_id)],
        ["PayloadLen", frame.header.payload_length + " bytes"],
        ["Checksum", frame.header.crc32_hex + (frame.crc32_valid ? " [VALID]" : " [INVALID]")],
        ["Schema 反射状态", frame.header.schema_decoded ? "dynamicpb 明文" : "Wire Tag 降级树"]
      ];
      if (frame.target_backend) {
        items.push(["Target Backend", frame.target_backend + (frame.backend_name ? " (" + frame.backend_name + ")" : "")]);
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

  function renderFrameTable() {
    var tbody = document.getElementById("frame-stream-tbody");
    if (!tbody) return;
    tbody.replaceChildren();
    setText("frame-count-label", "共 " + capturedFrames.length + " 帧 (点击任意行解剖)");

    var startIdx = Math.max(0, capturedFrames.length - 60);
    for (var i = capturedFrames.length - 1; i >= startIdx; i--) {
      (function (f) {
        var tr = el("tr", f.seq_id === selectedSeqID ? "active-row" : "");
        tr.appendChild(el("td", "", "#" + f.seq_id));
        tr.appendChild(el("td", "", f.direction));
        tr.appendChild(el("td", "", f.header.op_name));
        tr.appendChild(el("td", "", f.header.request_id));
        tr.appendChild(el("td", "", f.header.payload_length + "B"));
        tr.appendChild(el("td", "", f.header.crc32_hex));
        tr.appendChild(
          el("td", "", f.header.schema_decoded === false ? "TagFallback" : "dynamicpb")
        );
        tr.addEventListener("click", function () {
          renderFrameDetail(f);
          renderFrameTable();
        });
        tbody.appendChild(tr);
      })(capturedFrames[i]);
    }
  }

  function renderExperimentReport(report) {
    if (!report) return;
    renderEBPFMetrics(report.ebpf_status, null, null);

    var badge = document.getElementById("report-summary-badge");
    if (badge) {
      badge.className = "badge " + (report.overall_passed ? "badge-pass" : "badge-fail");
      badge.textContent =
        report.mode_description +
        " | 通过: " +
        report.passed_scenarios +
        "/" +
        report.total_scenarios +
        " | Mock 命中: " +
        report.mocks_consumed +
        "/" +
        report.mocks_loaded +
        " | 总耗时: " +
        report.duration_ms.toFixed(1) +
        "ms";
    }

    var backendNode = document.getElementById("topo-backend-node");
    if (backendNode) {
      backendNode.className = report.backend_online ? "topo-node" : "topo-node offline-node";
    }
    setText(
      "topo-backend-status",
      report.backend_online ? "127.0.0.1:9200 (ONLINE 真实进程)" : "127.0.0.1:9200 (OFFLINE 已关闭)"
    );
    setText(
      "topo-backend-link",
      report.backend_online ? "真实 TCP 二进制调用" : "🚫 已断开 (100% eBPF Mock)"
    );

    var container = document.getElementById("assertion-report-container");
    if (!container) return;
    container.replaceChildren();

    var scenarios = report.scenarios || [];
    for (var i = 0; i < scenarios.length; i++) {
      var sc = scenarios[i];
      var card = el("div", "scenario-card " + (sc.passed ? "pass" : "fail"));

      var head = el("div", "sc-head");
      head.appendChild(el("span", "sc-title", sc.scenario_id + " | " + sc.scenario_name));
      head.appendChild(
        el("span", "badge " + (sc.passed ? "badge-pass" : "badge-fail"), sc.passed ? "PASS" : "FAIL (DIFF)")
      );
      card.appendChild(head);

      card.appendChild(
        el(
          "div",
          "sc-meta",
          "ReqID=" +
            sc.request_id +
            " | CRC32: 预期=" +
            sc.expected_crc32 +
            " vs 实际=" +
            sc.actual_crc32 +
            " | " +
            sc.latency_ms.toFixed(2) +
            "ms"
        )
      );

      if (sc.error_message) {
        card.appendChild(el("div", "diff-list", "Error: " + sc.error_message));
      }

      if (sc.field_diffs && sc.field_diffs.length > 0) {
        var diffBox = el("div", "diff-list");
        for (var d = 0; d < sc.field_diffs.length; d++) {
          var df = sc.field_diffs[d];
          diffBox.appendChild(
            el("div", "diff-item", "⚠️ " + df.field + ": 预期 [" + df.expected + "] -> 实际 [" + df.actual + "]")
          );
        }
        card.appendChild(diffBox);
      }
      container.appendChild(card);
    }
  }

  function triggerExperiment(action) {
    var btns = document.querySelectorAll(".action-buttons .btn");
    for (var i = 0; i < btns.length; i++) {
      btns[i].disabled = true;
    }
    setText("last-action-hint", "正在执行实验 [" + action + "] ...");

    fetch("/api/run", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action: action })
    })
      .then(function (res) {
        return res.json();
      })
      .then(function (report) {
        renderExperimentReport(report);
        setText("last-action-hint", "实验 [" + action + "] 执行完成");
        loadInitialState();
      })
      .catch(function (err) {
        setText("last-action-hint", "执行失败: " + String(err));
      })
      .finally(function () {
        for (var j = 0; j < btns.length; j++) {
          btns[j].disabled = false;
        }
      });
  }

  function initSSE() {
    var es = new EventSource("/api/events");
    es.onopen = function () {
      setText("metric-sse-status", "实时连接");
    };
    es.onmessage = function (ev) {
      try {
        var frame = JSON.parse(ev.data);
        capturedFrames.push(frame);
        if (capturedFrames.length === 1 || selectedSeqID === null) {
          renderFrameDetail(frame);
        }
        renderFrameTable();
      } catch (ignore) {}
    };
    es.onerror = function () {
      setText("metric-sse-status", "重连中...");
    };
  }

  function loadInitialState() {
    fetch("/api/status")
      .then(function (r) {
        return r.json();
      })
      .then(function (data) {
        renderEBPFMetrics(data.ebpf_status, data.attached_target, data.framing_spec);
        renderSchemaStatus(data.schema_status);
        renderMockSelector(data.recorded_mocks || []);
        if (data.recent_frames && data.recent_frames.length > 0) {
          capturedFrames = data.recent_frames;
          var targetFrame = capturedFrames[capturedFrames.length - 1];
          for (var i = 0; i < capturedFrames.length; i++) {
            if (capturedFrames[i].seq_id === selectedSeqID) {
              targetFrame = capturedFrames[i];
              break;
            }
          }
          renderFrameDetail(targetFrame);
          renderFrameTable();
        }
        if (data.last_report) {
          renderExperimentReport(data.last_report);
        }
      })
      .catch(function () {});
  }

  document.addEventListener("DOMContentLoaded", function () {
    document.getElementById("btn-run-record").addEventListener("click", function () {
      triggerExperiment("record");
    });
    document.getElementById("btn-run-replay").addEventListener("click", function () {
      triggerExperiment("replay");
    });
    document.getElementById("btn-run-mutation").addEventListener("click", function () {
      triggerExperiment("mutation");
    });
    document.getElementById("btn-run-nondet").addEventListener("click", function () {
      triggerExperiment("non_deterministic");
    });

    document.getElementById("btn-refresh-procs").addEventListener("click", scanProcesses);
    document.getElementById("proc-search-input").addEventListener("input", scanProcesses);
    document.getElementById("btn-attach-custom").addEventListener("click", function () {
      attachSelectedProcess();
    });
    document.getElementById("btn-detach-proc").addEventListener("click", detachProcess);
    var addBackendBtn = document.getElementById("btn-add-backend");
    if (addBackendBtn) {
      addBackendBtn.addEventListener("click", addBackend);
    }
    document.getElementById("btn-apply-preset").addEventListener("click", applyFramingPreset);
    document.getElementById("btn-import-proto").addEventListener("click", function () {
      updateProtoSchema("import");
    });
    document.getElementById("btn-clear-proto").addEventListener("click", function () {
      updateProtoSchema("clear");
    });
    document.getElementById("btn-restore-proto").addEventListener("click", function () {
      updateProtoSchema("restore_default");
    });
    document.getElementById("select-mock-asset").addEventListener("change", syncMockEditorTextarea);
    document.getElementById("btn-save-mock").addEventListener("click", saveEditedMockJSON);

    loadInitialState();
    scanProcesses();
    initSSE();
  });
})();
