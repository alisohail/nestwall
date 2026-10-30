(function () {
  var STORAGE_KEY = 'nestwall.serverUrl';
  var form = document.getElementById('setup-form');
  var input = document.getElementById('server-url');
  var error = document.getElementById('error');
  var saved = localStorage.getItem(STORAGE_KEY);

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

  form.addEventListener('submit', function (event) {
    event.preventDefault();
    var server = normalise(input.value);
    if (!server) {
      error.textContent = 'Enter a full address that starts with http:// or https://';
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
})();
