(function () {
  var STORAGE_KEY = 'nestwall.serverUrl';
  var params = new URLSearchParams(window.location.search);
  var form = document.getElementById('setup-form');
  var input = document.getElementById('server-url');
  var error = document.getElementById('error');
  var saved = localStorage.getItem(STORAGE_KEY);

  // Normal launch with a saved server goes straight to it. "?change=1" shows the form.
  if (saved && !params.has('change')) {
    window.location.replace(saved);
    return;
  }

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
    // Plain http is refused: the app and the server both need HTTPS.
    if (url.protocol !== 'https:') {
      return null;
    }
    return url.origin + url.pathname.replace(/\/+$/, '');
  }

  form.addEventListener('submit', function (event) {
    event.preventDefault();
    var server = normalise(input.value);
    if (!server) {
      error.textContent = 'Enter a full address that starts with https://';
      input.focus();
      return;
    }
    localStorage.setItem(STORAGE_KEY, server);
    window.location.replace(server);
  });
})();
