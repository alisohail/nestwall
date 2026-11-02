package com.nestwall.app;

import android.os.Bundle;
import android.view.ViewGroup;
import android.webkit.WebView;
import com.getcapacitor.BridgeActivity;
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout;

public class MainActivity extends BridgeActivity {
  @Override
  public void onCreate(Bundle savedInstanceState) {
    registerPlugin(WatchPlugin.class);
    super.onCreate(savedInstanceState);
    installPullToRefresh();
  }

  // Wraps the Capacitor WebView so a downward swipe at the top of the page
  // clears the WebView and service worker caches, then reloads the app.
  private void installPullToRefresh() {
    final WebView webView = getBridge().getWebView();
    ViewGroup parent = (ViewGroup) webView.getParent();
    if (parent == null) {
      return;
    }
    int index = parent.indexOfChild(webView);
    ViewGroup.LayoutParams layoutParams = webView.getLayoutParams();

    parent.removeView(webView);
    final SwipeRefreshLayout swipeRefresh = new SwipeRefreshLayout(this);
    swipeRefresh.addView(webView, new ViewGroup.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
    parent.addView(swipeRefresh, index, layoutParams);

    swipeRefresh.setOnChildScrollUpCallback((layout, child) -> webView.getScrollY() > 0);
    swipeRefresh.setOnRefreshListener(() -> {
      swipeRefresh.setRefreshing(false);
      webView.clearCache(true);
      webView.evaluateJavascript(
          "(async () => {"
              + " if ('serviceWorker' in navigator) {"
              + "   const regs = await navigator.serviceWorker.getRegistrations();"
              + "   await Promise.all(regs.map(r => r.unregister()));"
              + " }"
              + " if (window.caches) {"
              + "   const keys = await caches.keys();"
              + "   await Promise.all(keys.map(k => caches.delete(k)));"
              + " }"
              + " location.reload();"
              + "})();",
          null);
    });
  }
}
