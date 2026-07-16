//go:build darwin && cgo

#import "metal.h"

#import <Metal/Metal.h>

#include <string.h>

int waydict_doctor_metal_device(char *name, size_t capacity) {
    @autoreleasepool {
        id<MTLDevice> device = MTLCreateSystemDefaultDevice();
        if (device == nil) {
            return 0;
        }
        const char *value = device.name.UTF8String;
        if (name != NULL && capacity > 0) {
            if (value == NULL) {
                name[0] = '\0';
            } else {
                strncpy(name, value, capacity - 1);
                name[capacity - 1] = '\0';
            }
        }
        return 1;
    }
}
