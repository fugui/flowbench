(function () {
  "use strict";

  var capturedFrames = [];
  var selectedSeqID = null;

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

  function byteCategoryClass(idx) {
    if (idx <= 1) return "byte-cell byte-magic";
    if (idx === 3) return "byte-cell byte-op";
    if (idx >= 8 && idx <= 15) return "byte-cell byte-reqid";
    if (idx >= 16 && idx <= 19) return "byte-cell byte-len";
    if (idx >= 20 && idx <= 23) return "byte-cell byte-crc";
    return "byte-cell";
  }

  function byteFieldName(idx) {
    if (idx <= 1) return "Magic";
    if (idx === 2) return "Ver";
    if (idx === 3) return "Op";
    if (idx === 4) return "Flag";
    if (idx === 5) return "Stat";
    if (idx <= 7) return "Rsv";
    if (idx <= 15) return "ReqID";
    if (idx <= 19) return "Len";
    return "CRC32";
  }

  function renderEBPFMetrics(status) {
    if (!status) return;
    setText("metric-mode", status.mode || "UNKNOWN");
    setText("metric-prog-id", status.program_id ? "#" + status.program_id : "Userspace");
    setText("metric-ingress-cnt", status.stat_ingress_redirected || 0);
    setText("metric-egress-cnt", status.stat_egress_redirected || 0);
  }

  function renderFrameDetail(frame) {
    if (!frame) return;
    selectedSeqID = frame.seq_id;
    setText(
      "selected-frame-title",
      "#" + frame.seq_id + " | " + frame.direction + " | " + frame.header.op_name + " (ReqID=" + frame.header.request_id + ")"
    );

    var matrix = document.getElementById("header-byte-matrix");
    if (matrix) {
      matrix.replaceChildren();
      var hexArr = frame.header_bytes_hex || [];
      for (var i = 0; i < 24; i++) {
        var cell = el("div", byteCategoryClass(i));
        var offLabel = el("span", "b-off", (i < 10 ? "0" + i : String(i)) + ":" + byteFieldName(i));
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
        ["Magic (0..1)", frame.header.magic_hex],
        ["Version (2)", String(frame.header.version)],
        ["OpCode (3)", frame.header.op_code_hex + " (" + frame.header.op_name + ")"],
        ["Flags / Status", frame.header.flags_hex + " / " + frame.header.status_name],
        ["RequestID (8..15)", String(frame.header.request_id)],
        ["PayloadLen (16..19)", frame.header.payload_length + " bytes"],
        ["CRC32 (20..23)", frame.header.crc32_hex + (frame.crc32_valid ? " [VALID]" : " [INVALID]")],
        ["Mode / Latency", frame.mode + " / " + frame.latency_ms.toFixed(2) + " ms"]
      ];
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
      "[Wire Hex (" + (frame.raw_wire_hex ? frame.raw_wire_hex.length / 2 : 0) + "B)]:\n" +
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
        tr.appendChild(el("td", "", f.latency_ms.toFixed(2) + "ms"));
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
    renderEBPFMetrics(report.ebpf_status);

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
    var btns = document.querySelectorAll(".btn");
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
        renderEBPFMetrics(data.ebpf_status);
        if (data.recent_frames && data.recent_frames.length > 0) {
          capturedFrames = data.recent_frames;
          renderFrameDetail(capturedFrames[capturedFrames.length - 1]);
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

    loadInitialState();
    initSSE();
  });
})();
