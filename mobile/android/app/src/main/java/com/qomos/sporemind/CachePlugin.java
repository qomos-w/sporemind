package com.qomos.sporemind;

import android.webkit.WebView;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

/**
 * Exposes WebView cache clearing to the mobile login shell.
 *
 * The shell loads the remote web app in an iframe; when the user enables
 * "no cache" we need to drop the WebView's HTTP cache so the next iframe
 * load fetches a fresh index.html. JavaScript cannot do this itself.
 */
@CapacitorPlugin(name = "MyxosCache")
public class CachePlugin extends Plugin {

    @PluginMethod
    public void clearCache(PluginCall call) {
        WebView webView = getBridge().getWebView();
        if (webView == null) {
            call.reject("WebView not available");
            return;
        }
        getActivity().runOnUiThread(() -> {
            // true includes disk files; this clears cache for the whole app,
            // which is fine because the Capacitor app has only this WebView.
            webView.clearCache(true);
            call.resolve();
        });
    }
}
