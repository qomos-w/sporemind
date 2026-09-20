package com.qomos.sporemind;

import android.content.Intent;
import android.graphics.Color;
import android.os.Build;
import android.os.Bundle;
import android.util.Log;
import android.view.MotionEvent;
import android.view.View;
import androidx.core.view.ViewCompat;
import androidx.core.view.WindowCompat;
import androidx.core.view.WindowInsetsCompat;
import androidx.core.graphics.Insets;
import androidx.core.view.WindowInsetsControllerCompat;
import com.getcapacitor.BridgeActivity;
import com.getcapacitor.PluginHandle;

public class MainActivity extends BridgeActivity {

    private static final String TAG = "MainActivity";
    private TouchMonitorPlugin touchMonitor;
    private boolean touchMonitorResolved = false;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        // Add to initialPlugins BEFORE super.onCreate() calls load() → create().
        // This ensures JSExport.getPluginJS() generates the TouchMonitor proxy.
        initialPlugins.add(TouchMonitorPlugin.class);
        initialPlugins.add(CachePlugin.class);

        WindowCompat.setDecorFitsSystemWindows(getWindow(), false);
        super.onCreate(savedInstanceState);

        // Hide status bar, keep bottom gesture nav
        WindowInsetsControllerCompat controller =
            new WindowInsetsControllerCompat(getWindow(), getWindow().getDecorView());
        controller.hide(WindowInsetsCompat.Type.statusBars());
        controller.setSystemBarsBehavior(
            WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        );

        // Make the navigation bar transparent so the WebView shows through it.
        // On Android 10+ also disable the mandatory gesture-bar contrast scrim.
        getWindow().setNavigationBarColor(Color.TRANSPARENT);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            getWindow().setNavigationBarContrastEnforced(false);
        }

        View root = findViewById(android.R.id.content);

        // Handle display-cutout / gesture-nav insets for edge-to-edge layout
        ViewCompat.setOnApplyWindowInsetsListener(root, (v, insets) -> {
            Insets sysBars = insets.getInsets(
                WindowInsetsCompat.Type.systemBars() | WindowInsetsCompat.Type.displayCutout()
            );
            // Let the WebView draw under the system navigation bar; the web
            // content will use env(safe-area-inset-bottom) to avoid it.
            v.setPadding(sysBars.left, 0, sysBars.right, 0);
            return WindowInsetsCompat.CONSUMED;
        });
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
    }

    @Override
    public boolean dispatchTouchEvent(MotionEvent ev) {
        if (!touchMonitorResolved) {
            try {
                if (getBridge() != null) {
                    PluginHandle handle = getBridge().getPlugin("TouchMonitor");
                    if (handle != null) {
                        touchMonitor = (TouchMonitorPlugin) handle.getInstance();
                        if (touchMonitor != null) {
                            touchMonitorResolved = true;
                            Log.d(TAG, "TouchMonitor resolved OK");
                        }
                    }
                }
            } catch (Exception e) {
                Log.w(TAG, "TouchMonitor resolve failed: " + e.getMessage());
            }
        }
        if (touchMonitor != null) {
            touchMonitor.onDispatchTouchEvent(ev);
        }
        return super.dispatchTouchEvent(ev);
    }
}
