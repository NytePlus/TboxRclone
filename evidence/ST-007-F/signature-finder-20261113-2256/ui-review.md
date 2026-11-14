# Signature-expiry fault: partial ST-007-F evidence

The carried-forward review covered the one-second samples t=0..59 of
finder/display-1.mov: single-file Finder drag at approximately 6s, preparation /
copy progress at 11..49s, dialog gone at 50s and a normal destination entry by
51s. This continuation reviewed sheets review.60.png, review.70.png and
review.80.png (t=60..89): source and destination entries remain visible with no
copy-progress or error dialog in those samples. This is one-second sampling,
not a claim to have inspected every video frame.

The fault is one synthetic HTTP 403 on part 2 (request 266, sequence 794),
not proof of a signed URL actually expiring with elapsed time. Subsequent events
show GET 267 and POST 268 to the service, then PUT 269 to object storage with
origin status 200. The sanitized trace omits request paths, so that trace alone
does not identify the API operation behind each service request.

reopen.json records matching source, mounted reopen and independent cloud SHA-256
for 9,437,184 bytes: b8b5dabaa3454f7fa46a97c51cec9ccd118ffc0d61df6e46d845ef200a2fd1bb.

ST-007-F remains incomplete: session_expired and replay_identical_part still need
their required evidence. This review does not change its manifest status.
