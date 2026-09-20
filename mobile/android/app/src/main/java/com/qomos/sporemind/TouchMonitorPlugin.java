package com.qomos.sporemind;

import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;
import android.util.Log;
import android.view.MotionEvent;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

@CapacitorPlugin(name = "TouchMonitor")
public class TouchMonitorPlugin extends Plugin {

    private static final String TAG = "TouchMonitor";
    private final Handler handler = new Handler(Looper.getMainLooper());
    private boolean fingerDown;
    private long fingerDownAt;
    private boolean armed;
    private boolean active;
    private String gestureId;
    private Runnable activation;

    @Override
    public void load() {
        Log.d(TAG, "load() called, bridge=" + getBridge());
    }

    @PluginMethod
    public void armPushToTalk(PluginCall call) {
        String nextGestureId = call.getString("gestureId");
        int thresholdMs = call.getInt("thresholdMs", 420);
        if (!fingerDown || nextGestureId == null) {
            JSObject result = new JSObject();
            result.put("accepted", false);
            call.resolve(result);
            return;
        }

        resetGesture(false);
        armed = true;
        gestureId = nextGestureId;
        long delay = Math.max(0, thresholdMs - (SystemClock.uptimeMillis() - fingerDownAt));
        activation = () -> {
            if (!armed || !fingerDown || active) return;
            active = true;
            emitState(true);
        };
        handler.postDelayed(activation, delay);
        JSObject result = new JSObject();
        result.put("accepted", true);
        call.resolve(result);
    }

    public void onDispatchTouchEvent(MotionEvent ev) {
        int action = ev.getActionMasked();
        if (action == MotionEvent.ACTION_DOWN) {
            fingerDown = true;
            fingerDownAt = SystemClock.uptimeMillis();
            return;
        }
        if (action == MotionEvent.ACTION_UP || action == MotionEvent.ACTION_CANCEL) {
            fingerDown = false;
            if (!armed) return;
            if (active) {
                emitState(false);
            } else {
                JSObject data = new JSObject();
                data.put("gestureId", gestureId);
                notifyListeners("pushToTalkTap", data);
            }
            resetGesture(false);
        }
    }

    private void emitState(boolean nextActive) {
        JSObject data = new JSObject();
        data.put("gestureId", gestureId);
        data.put("active", nextActive);
        notifyListeners("pushToTalkState", data);
    }

    private void resetGesture(boolean emitInactive) {
        if (activation != null) {
            handler.removeCallbacks(activation);
            activation = null;
        }
        if (emitInactive && active) emitState(false);
        armed = false;
        active = false;
        gestureId = null;
    }
}
