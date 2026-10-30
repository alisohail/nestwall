(function () {
  var STORAGE_KEY = 'nestwall.serverUrl';
  var form = document.getElementById('setup-form');
  var input = document.getElementById('server-url');
  var error = document.getElementById('error');
  var saved = localStorage.getItem(STORAGE_KEY);
  var autoOpen = !!saved && !new URLSearchParams(location.search).has('change');
  var button = form.querySelector('button');
  var CHECK_TIMEOUT_MS = 8000;
  var checking = false;

  if (saved) {
    input.value = saved;
  }

  function normalise(value) {
    var trimmed = value.trim().replace(/\/+$/, '');
    var url;
    try {
      url = new URL(trimmed);
    } catch (e) {
      return null;
    }
    // Accept both http and https so LAN servers without a certificate work.
    if (url.protocol !== 'https:' && url.protocol !== 'http:') {
      return null;
    }
    return url.origin + url.pathname.replace(/\/+$/, '');
  }

  function reveal() {
    document.documentElement.style.visibility = '';
  }

  function setBusy(busy) {
    checking = busy;
    button.disabled = busy;
    button.textContent = busy ? 'Checking…' : 'Connect';
  }

  // A no-cors request resolves for any HTTP answer and rejects only when the server cannot be reached.
  function reachable(server) {
    var controller = new AbortController();
    var timer = setTimeout(function () {
      controller.abort();
    }, CHECK_TIMEOUT_MS);
    return fetch(server + '/', { mode: 'no-cors', cache: 'no-store', signal: controller.signal })
      .then(function () {
        return true;
      }, function () {
        return false;
      })
      .finally(function () {
        clearTimeout(timer);
      });
  }

  function connect(server) {
    if (checking) {
      return;
    }
    error.textContent = '';
    setBusy(true);
    reachable(server).then(function (ok) {
      setBusy(false);
      if (!ok) {
        error.textContent = "Can't reach " + server + '. Check that the server is running and the address is correct, then try again.';
        reveal();
        input.focus();
        return;
      }
      localStorage.setItem(STORAGE_KEY, server);
      var watch = window.Capacitor && window.Capacitor.Plugins && window.Capacitor.Plugins.NestWallWatch;
      if (watch && server !== saved) {
        watch.stop();
      }
      window.location.replace(server);
    });
  }

  form.addEventListener('submit', function (event) {
    event.preventDefault();
    var server = normalise(input.value);
    if (!server) {
      error.textContent = 'Enter a full address that starts with http:// or https://';
      input.focus();
      return;
    }
    connect(server);
  });

  if (autoOpen) {
    connect(saved);
  } else {
    reveal();
  }
})();
