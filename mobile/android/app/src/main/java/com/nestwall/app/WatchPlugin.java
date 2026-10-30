package com.nestwall.app;

import android.Manifest;
import android.content.Intent;
import androidx.core.content.ContextCompat;
import com.getcapacitor.PermissionState;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.Permission;
import com.getcapacitor.annotation.PermissionCallback;

/** Lets the web app start and stop the foreground watcher that delivers alerts while the app is closed. */
@CapacitorPlugin(
    name = "NestWallWatch",
    permissions = {
      @Permission(alias = "notifications", strings = { Manifest.permission.POST_NOTIFICATIONS })
    })
public class WatchPlugin extends Plugin {

  @PluginMethod
  public void start(PluginCall call) {
    String serverUrl = call.getString("serverUrl");
    if (serverUrl == null || !(serverUrl.startsWith("https://") || serverUrl.startsWith("http://"))) {
      call.reject("serverUrl must start with http:// or https://");
      return;
    }
    if (getPermissionState("notifications") != PermissionState.GRANTED) {
      requestPermissionForAlias("notifications", call, "onNotificationsPermission");
      return;
    }
    launchWatcher(serverUrl);
    call.resolve();
  }

  @PermissionCallback
  private void onNotificationsPermission(PluginCall call) {
    // The watcher runs either way. Without permission it simply shows no alerts.
    launchWatcher(call.getString("serverUrl"));
    call.resolve();
  }

  @PluginMethod
  public void stop(PluginCall call) {
    Intent intent = new Intent(getContext(), WatchService.class).setAction(WatchService.ACTION_STOP);
    getContext().startService(intent);
    call.resolve();
  }

  private void launchWatcher(String serverUrl) {
    Intent intent = new Intent(getContext(), WatchService.class).putExtra(WatchService.EXTRA_URL, serverUrl);
    ContextCompat.startForegroundService(getContext(), intent);
  }
}
