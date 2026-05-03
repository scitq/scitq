-- Phase II of the worker version-awareness feature: operator-triggered
-- upgrade. The server records a pending upgrade request per worker; the
-- worker reads the column from its ping response and acts on it. See
-- specs/worker_autoupgrade.md.
--
-- NULL    = no request
-- 'normal'    = idle-wait then upgrade
-- 'emergency' = drain in-flight tasks (30-min hard cap) then upgrade
ALTER TABLE worker
    ADD COLUMN upgrade_requested TEXT;
