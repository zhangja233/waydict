#ifndef WAYDICT_PERMISSIONS_H
#define WAYDICT_PERMISSIONS_H

typedef enum {
    WaydictPermissionStateNotDetermined = 0,
    WaydictPermissionStateNotGranted = 1,
    WaydictPermissionStateGranted = 2,
    WaydictPermissionStateDenied = 3,
    WaydictPermissionStateRestricted = 4,
    WaydictPermissionStateUnavailable = 5,
} WaydictPermissionState;

typedef enum {
    WaydictPermissionKindMicrophone = 0,
    WaydictPermissionKindAccessibility = 1,
    WaydictPermissionKindInputMonitoring = 2,
} WaydictPermissionKind;

typedef enum {
    WaydictPermissionResultOK = 0,
    WaydictPermissionResultInvalidKind = 1,
    WaydictPermissionResultOpenSettingsFailed = 2,
} WaydictPermissionResult;

typedef struct {
    int microphone;
    int accessibility;
    int input_monitoring;
} waydict_permission_snapshot_t;

void waydict_permissions_snapshot(waydict_permission_snapshot_t *snapshot);
int waydict_permissions_request(int kind, int *state);
int waydict_permissions_open_settings(int kind);

#endif
