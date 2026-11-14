# Direct Finder locking and post-restart save — PASS

The 90.018-second capture completed its requested 90-second duration. Reviewed
one-second sheets 0..9, 60..79 and 90, selected from drag and completion events
and the server request timeline. Other recorded intervals remain available;
this is an event-focused review, not a claim that all 91 samples were inspected.
Drag occurs at 7..8s. Progress is visible through 68s; at 69s it disappears,
the target becomes visible at 10 KB, and no error dialog appears in the reviewed
completion interval or final frame. The source remains present. Directory
loading is slow during the copy; this run does not establish performance acceptance.

requests.log contains the native client's main-file LOCK at 17:24:13, PUT at
17:24:44, UNLOCK at 17:24:44 and a subsequent LOCK/GET/UNLOCK cycle. The log
records methods, not raw If header values. Valid-token predicate behavior is
independently covered by the direct protocol supplement; no raw-token claim
is inferred from the INFO log. Finder also attempts an AppleDouble DELETE;
lab_delete=false remained enforced. Metadata preservation is a separate case.

verification/reopen.json confirms source, verified mounted full read and
independent direct cloud full read: 9984 bytes and SHA-256
4e6d4877145f0a2599f4a7106a4854faeec13aeaaa73f49821ec07f72e064452.
This save was performed after the service restart and verified remount recorded
in ST-012-F/restart-locks-20261114-0121/restart.log. That test establishes old
lock rejection before expiry and acceptance of a newly acquired lock.

The earlier 30.585-second recording remains retained as incomplete evidence.
No additional screen-reading tool was invoked during this full recording;
this single comparison does not establish the cause of the earlier early stop.
