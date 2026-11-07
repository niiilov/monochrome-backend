(function () {
  var runID = "";
  var timer = 0;
  var mail = { text: "", html: "" };
  var tab = "digest";

  var msg = document.getElementById("msg");
  var progress = document.getElementById("progress");
  var stages = document.getElementById("stages");
  var paneText = document.getElementById("pane-text");
  var paneMail = document.getElementById("pane-mail");
  var exportBtn = document.getElementById("export-btn");
  var sendBtn = document.getElementById("send-btn");
  var articleBtn = document.getElementById("article-btn");

  function say(text, kind) {
    msg.textContent = text || "";
    msg.className = kind || "";
  }

  function setBusy(on) {
    exportBtn.classList.toggle("disabled", !on);
    if (on) {
      exportBtn.href = "/api/v1/runs/" + encodeURIComponent(runID) + "/export";
    } else {
      exportBtn.href = "#";
    }
    sendBtn.disabled = !on;
    articleBtn.disabled = !on;
  }

  function showTab(name) {
    tab = name;
    var buttons = document.querySelectorAll(".tabs button");
    for (var i = 0; i < buttons.length; i++) {
      buttons[i].classList.toggle("active", buttons[i].getAttribute("data-tab") === name);
    }
    if (name === "email") {
      paneText.hidden = false;
      paneMail.hidden = false;
      paneText.textContent = mail.text || "Текст письма пуст.";
      paneMail.srcdoc = mail.html || "<p>HTML письма пуст.</p>";
      return;
    }
    paneMail.hidden = true;
    paneText.hidden = false;
  }

  function stopPoll() {
    if (timer) {
      clearInterval(timer);
      timer = 0;
    }
  }

  function renderStatus(data) {
    var line = "Статус: " + (data.status || "");
    if (data.stage) {
      line += ", этап " + data.stage;
      if (data.total) {
        line += " (" + data.done + "/" + data.total + ")";
      }
    }
    if (data.parent_run_id) {
      line += ". Перезапуск после добавления публикации.";
    }
    progress.textContent = line;
    document.getElementById("run-id").textContent = data.run_id ? "Прогон " + data.run_id : "";
    stages.textContent = "";
    var list = data.stages || [];
    for (var i = 0; i < list.length; i++) {
      var item = document.createElement("li");
      item.textContent = list[i].name + " " + list[i].done + "/" + list[i].total;
      stages.appendChild(item);
    }
    if (data.status === "failed") {
      say(data.error || "Прогон завершился с ошибкой", "error");
    }
  }

  function loadPreview() {
    var base = "/api/v1/runs/" + encodeURIComponent(runID);
    Promise.all([
      fetch(base + "/digest", { headers: { Accept: "text/markdown" } }).then(readText),
      fetch(base + "/post").then(readText),
      fetch(base + "/email", { headers: { Accept: "application/json" } }).then(readJSON),
      fetch(base + "/journal").then(readText)
    ]).then(function (parts) {
      window.__preview = {
        digest: parts[0],
        post: parts[1],
        email: parts[2],
        journal: parts[3]
      };
      mail.text = parts[2].text || "";
      mail.html = parts[2].html || "";
      paint();
    }).catch(function (err) {
      say(err.message || "Не удалось загрузить предпросмотр", "error");
    });
  }

  function paint() {
    var data = window.__preview || {};
    if (tab === "digest") {
      paneText.textContent = data.digest || "";
    } else if (tab === "post") {
      paneText.textContent = data.post || "";
    } else if (tab === "journal") {
      paneText.textContent = data.journal || "";
    }
    showTab(tab);
  }

  function readText(res) {
    return res.text().then(function (text) {
      if (!res.ok) {
        throw new Error(errorText(text));
      }
      return text;
    });
  }

  function readJSON(res) {
    return res.text().then(function (text) {
      var data = {};
      if (text) {
        try { data = JSON.parse(text); } catch (e) { data = { error: text }; }
      }
      if (!res.ok) {
        throw new Error(data.error || text || "ошибка запроса");
      }
      return data;
    });
  }

  function errorText(text) {
    try {
      var data = JSON.parse(text);
      if (data.error) return data.error;
    } catch (e) { /* plain text */ }
    return text || "ошибка запроса";
  }

  function poll() {
    fetch("/api/v1/runs/" + encodeURIComponent(runID))
      .then(readJSON)
      .then(function (data) {
        renderStatus(data);
        if (data.status === "done") {
          stopPoll();
          setBusy(true);
          say("Прогон готов", "ok");
          loadPreview();
        } else if (data.status === "failed") {
          stopPoll();
          setBusy(false);
        }
      })
      .catch(function (err) {
        stopPoll();
        say(err.message, "error");
      });
  }

  function watch(id) {
    runID = id;
    setBusy(false);
    mail = { text: "", html: "" };
    window.__preview = {};
    paneText.textContent = "Ждем результат прогона.";
    stopPoll();
    poll();
    timer = setInterval(poll, 400);
  }

  document.getElementById("run-form").addEventListener("submit", function (ev) {
    ev.preventDefault();
    var path = document.getElementById("dataset-path").value.trim();
    var file = document.getElementById("dataset-file").files[0];
    var control = document.getElementById("control-time").value.trim();
    if (!path && !file) {
      say("Укажите путь или выберите файл", "error");
      return;
    }
    var body = new FormData();
    if (file) body.append("file", file);
    if (path) body.append("dataset", path);
    body.append("control_time", control);
    say("Запускаем прогон", "");
    fetch("/api/v1/runs", { method: "POST", body: body })
      .then(readJSON)
      .then(function (data) { watch(data.run_id); })
      .catch(function (err) { say(err.message, "error"); });
  });

  document.querySelector(".tabs").addEventListener("click", function (ev) {
    var button = ev.target.closest("button");
    if (!button) return;
    showTab(button.getAttribute("data-tab"));
    paint();
  });

  exportBtn.addEventListener("click", function (ev) {
    if (exportBtn.classList.contains("disabled")) ev.preventDefault();
  });

  document.getElementById("send-form").addEventListener("submit", function (ev) {
    ev.preventDefault();
    if (!runID) return;
    var raw = document.getElementById("recipients").value.trim();
    var list = raw ? raw.split(/[\s,;]+/).filter(Boolean) : [];
    fetch("/api/v1/runs/" + encodeURIComponent(runID) + "/send", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        recipients: list,
        dry_run: document.getElementById("dry-run").checked
      })
    }).then(readJSON).then(function (data) {
      var who = (data.recipients || []).join(", ");
      say("Отправка: " + data.result + (who ? " (" + who + ")" : ""), "ok");
    }).catch(function (err) {
      say(err.message, "error");
    });
  });

  document.getElementById("article-form").addEventListener("submit", function (ev) {
    ev.preventDefault();
    if (!runID) return;
    var text = document.getElementById("article-text").value.trim();
    var file = document.getElementById("article-file").files[0];
    var title = document.getElementById("article-title").value.trim();
    var req;
    if (file) {
      var body = new FormData();
      body.append("file", file);
      if (text) body.append("text", text);
      if (title) body.append("title", title);
      req = fetch("/api/v1/runs/" + encodeURIComponent(runID) + "/articles", { method: "POST", body: body });
    } else if (text) {
      req = fetch("/api/v1/runs/" + encodeURIComponent(runID) + "/articles", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          title: title,
          text: text,
          source: "демо",
          url: "https://example.com/demo",
          published_at: "2026-11-07T11:00:00+03:00"
        })
      });
    } else {
      say("Введите текст или выберите файл публикации", "error");
      return;
    }
    req.then(readJSON).then(function (data) {
      say("Новый прогон " + data.run_id, "ok");
      watch(data.run_id);
    }).catch(function (err) {
      say(err.message, "error");
    });
  });

  showTab("digest");
  setBusy(false);
})();
