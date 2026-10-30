package com.nestwall.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;
import android.webkit.CookieManager;
import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;
import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Iterator;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

/**
 * Foreground service that holds the server's notification stream open and posts a native alert for each new
 * unread notice, so alerts arrive while the app is closed. It reuses the same endpoints as the in-app bell.
 */
public class WatchService extends Service {
  static final String ACTION_STOP = "com.nestwall.app.WATCH_STOP";
  static final String EXTRA_URL = "serverUrl";

  private static final String PREFS = "nestwall_watch";
  private static final String KEY_URL = "serverUrl";
  private static final String KEY_SEEN = "seenIds";
  private static final int SEEN_LIMIT = 300;
  private static final int MAX_ALERTS_PER_UPDATE = 5;
  private static final int FOREGROUND_ID = 4101;
  private static final String CHANNEL_WATCH = "nestwall_watch";
  private static final String CHANNEL_ALERTS = "nestwall_alerts";
  private static final long MIN_BACKOFF_MS = 2_000;
  private static final long MAX_BACKOFF_MS = 60_000;
  private static final int CONNECT_TIMEOUT_MS = 15_000;
  // The server sends a heartbeat every 25 seconds, so a longer silence means the connection is dead.
  private static final int READ_TIMEOUT_MS = 60_000;

  private final Set<String> seen = new LinkedHashSet<>();
  private boolean baselined;
  private volatile boolean running;
  private volatile HttpURLConnection active;
  private Thread worker;
  private String serverUrl;

  @Override
  public IBinder onBind(Intent intent) {
    return null;
  }

  @Override
  public int onStartCommand(Intent intent, int flags, int startId) {
    if (intent != null && ACTION_STOP.equals(intent.getAction())) {
      stopWatching();
      return START_NOT_STICKY;
    }
    String url = intent != null ? intent.getStringExtra(EXTRA_URL) : null;
    if (url == null) url = prefs().getString(KEY_URL, null);
    if (url == null) {
      stopSelf();
      return START_NOT_STICKY;
    }

    createChannels();
    startForegroundCompat();
    if (worker != null && worker.isAlive() && url.equals(serverUrl)) return START_STICKY;

    stopLoop();
    // A different server has its own inbox, so it starts from a fresh baseline.
    if (!url.equals(prefs().getString(KEY_URL, null))) prefs().edit().remove(KEY_SEEN).apply();
    prefs().edit().putString(KEY_URL, url).apply();
    serverUrl = url;
    loadSeen();
    running = true;
    worker = new Thread(this::watchLoop, "nestwall-watch");
    worker.start();
    return START_STICKY;
  }

  @Override
  public void onDestroy() {
    stopLoop();
    super.onDestroy();
  }

  private void watchLoop() {
    long backoff = MIN_BACKOFF_MS;
    while (running) {
      try {
        streamOnce();
        backoff = MIN_BACKOFF_MS;
      } catch (SignedOut e) {
        stopWatching();
        return;
      } catch (Exception e) {
        backoff = Math.min(backoff * 2, MAX_BACKOFF_MS);
      }
      if (!running) return;
      try {
        Thread.sleep(backoff);
      } catch (InterruptedException e) {
        return;
      }
    }
  }

  private void streamOnce() throws IOException, JSONException {
    String cookie = sessionCookie();
    HttpURLConnection c = open("/api/v1/notifications/stream", cookie);
    try {
      checkStatus(c);
      active = c;
      fetchAndAlert(cookie); // Catch up on connect, as the bell does.
      try (BufferedReader reader = new BufferedReader(new InputStreamReader(c.getInputStream(), StandardCharsets.UTF_8))) {
        String line;
        while (running && (line = reader.readLine()) != null) {
          if (line.equals("event: changed")) fetchAndAlert(cookie);
        }
      }
    } finally {
      active = null;
      c.disconnect();
    }
  }

  /** Reads the inbox and posts alerts for unread notices this device has not reported yet. */
  private synchronized void fetchAndAlert(String cookie) throws IOException, JSONException {
    HttpURLConnection c = open("/api/v1/notifications", cookie);
    String body;
    try {
      checkStatus(c);
      body = readAll(c.getInputStream());
    } finally {
      c.disconnect();
    }

    JSONObject root = new JSONObject(body);
    JSONArray list = root.getJSONArray("notifications");
    boolean alertsEnabled = root.optBoolean("browser_enabled", false);
    boolean firstLoad = !baselined;
    List<JSONObject> fresh = new ArrayList<>();
    for (int i = 0; i < list.length(); i++) {
      JSONObject item = list.getJSONObject(i);
      String id = item.getString("id");
      if (!firstLoad && !seen.contains(id) && item.isNull("read_at")) fresh.add(item);
      seen.add(id);
    }
    baselined = true;
    saveSeen();

    if (firstLoad || !alertsEnabled) return;
    for (int i = 0; i < fresh.size() && i < MAX_ALERTS_PER_UPDATE; i++) {
      JSONObject item = fresh.get(i);
      showAlert(item.getString("id"), item.optString("body", ""));
    }
  }

  private String sessionCookie() throws SignedOut {
    String cookie = CookieManager.getInstance().getCookie(serverUrl);
    if (cookie == null || cookie.isEmpty()) throw new SignedOut();
    return cookie;
  }

  private HttpURLConnection open(String path, String cookie) throws IOException {
    HttpURLConnection c = (HttpURLConnection) new URL(serverUrl + path).openConnection();
    c.setConnectTimeout(CONNECT_TIMEOUT_MS);
    c.setReadTimeout(READ_TIMEOUT_MS);
    c.setRequestProperty("Cookie", cookie);
    c.setRequestProperty("Accept", path.endsWith("/stream") ? "text/event-stream" : "application/json");
    return c;
  }

  private static void checkStatus(HttpURLConnection c) throws IOException {
    int code = c.getResponseCode();
    if (code == 401 || code == 403) throw new SignedOut();
    if (code != 200) throw new IOException("NestWall answered " + code);
  }

  private void showAlert(String id, String text) {
    if (!NotificationManagerCompat.from(this).areNotificationsEnabled()) return;
    Intent open = new Intent(this, MainActivity.class).setFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TOP);
    PendingIntent tap = PendingIntent.getActivity(
        this, id.hashCode(), open, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    Notification n = new NotificationCompat.Builder(this, CHANNEL_ALERTS)
        .setSmallIcon(R.drawable.ic_stat_nestwall)
        .setContentTitle(getString(R.string.app_name))
        .setContentText(text)
        .setStyle(new NotificationCompat.BigTextStyle().bigText(text))
        .setContentIntent(tap)
        .setAutoCancel(true)
        .build();
    NotificationManagerCompat.from(this).notify(id.hashCode(), n);
  }

  private void startForegroundCompat() {
    Notification n = new NotificationCompat.Builder(this, CHANNEL_WATCH)
        .setSmallIcon(R.drawable.ic_stat_nestwall)
        .setContentTitle(getString(R.string.app_name))
        .setContentText(getString(R.string.watch_notice))
        .setOngoing(true)
        .setPriority(NotificationCompat.PRIORITY_LOW)
        .build();
    if (Build.VERSION.SDK_INT >= 34) {
      startForeground(FOREGROUND_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
    } else {
      startForeground(FOREGROUND_ID, n);
    }
  }

  private void createChannels() {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return;
    NotificationManager manager = getSystemService(NotificationManager.class);
    manager.createNotificationChannel(
        new NotificationChannel(CHANNEL_WATCH, getString(R.string.watch_channel_name), NotificationManager.IMPORTANCE_LOW));
    manager.createNotificationChannel(
        new NotificationChannel(CHANNEL_ALERTS, getString(R.string.alerts_channel_name), NotificationManager.IMPORTANCE_DEFAULT));
  }

  private void stopLoop() {
    running = false;
    HttpURLConnection c = active;
    if (c != null) c.disconnect();
    if (worker != null) worker.interrupt();
    worker = null;
  }

  private void stopWatching() {
    stopLoop();
    prefs().edit().remove(KEY_URL).remove(KEY_SEEN).apply();
    stopForeground(true);
    stopSelf();
  }

  private synchronized void loadSeen() {
    seen.clear();
    String stored = prefs().getString(KEY_SEEN, null);
    if (stored != null && !stored.isEmpty()) seen.addAll(Arrays.asList(stored.split(",")));
    baselined = prefs().contains(KEY_SEEN);
  }

  private void saveSeen() {
    Iterator<String> it = seen.iterator();
    while (seen.size() > SEEN_LIMIT && it.hasNext()) {
      it.next();
      it.remove();
    }
    StringBuilder joined = new StringBuilder();
    for (String id : seen) {
      if (joined.length() > 0) joined.append(',');
      joined.append(id);
    }
    prefs().edit().putString(KEY_SEEN, joined.toString()).apply();
  }

  private SharedPreferences prefs() {
    return getSharedPreferences(PREFS, MODE_PRIVATE);
  }

  private static String readAll(InputStream in) throws IOException {
    StringBuilder out = new StringBuilder();
    try (BufferedReader reader = new BufferedReader(new InputStreamReader(in, StandardCharsets.UTF_8))) {
      char[] buf = new char[4096];
      int n;
      while ((n = reader.read(buf)) != -1) out.append(buf, 0, n);
    }
    return out.toString();
  }

  /** The browser session is gone, so the watcher stops until the app signs in again. */
  private static final class SignedOut extends IOException {
    SignedOut() {
      super("not signed in");
    }
  }
}
