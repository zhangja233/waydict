//go:build darwin && cgo

#import "loginitem.h"

#import <Foundation/Foundation.h>
#import <ServiceManagement/ServiceManagement.h>

#include <stdlib.h>
#include <string.h>

static void waydict_set_error(char **error_message, NSError *error) {
    if (error_message == NULL) {
        return;
    }
    NSString *message = error == nil
        ? @"unknown ServiceManagement error"
        : [NSString stringWithFormat:@"%@ (%ld): %@", error.domain, (long)error.code, error.localizedDescription];
    const char *value = message.UTF8String;
    *error_message = strdup(value == NULL ? "unknown ServiceManagement error" : value);
}

int waydict_loginitem_status(void) {
    @autoreleasepool {
        return (int)SMAppService.mainAppService.status;
    }
}

int waydict_loginitem_set_enabled(int enabled, char **error_message) {
    if (error_message != NULL) {
        *error_message = NULL;
    }
    @autoreleasepool {
        SMAppService *service = SMAppService.mainAppService;
        SMAppServiceStatus status = service.status;
        if ((enabled != 0 && status == SMAppServiceStatusEnabled)
            || (enabled == 0 && status == SMAppServiceStatusNotRegistered)) {
            return 1;
        }

        NSError *error = nil;
        BOOL succeeded = enabled != 0
            ? [service registerAndReturnError:&error]
            : [service unregisterAndReturnError:&error];
        if (!succeeded) {
            waydict_set_error(error_message, error);
        }
        return succeeded ? 1 : 0;
    }
}

void waydict_loginitem_free_error(char *error_message) {
    free(error_message);
}
