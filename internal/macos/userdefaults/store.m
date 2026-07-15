//go:build darwin && cgo

#import "store.h"

#import <Foundation/Foundation.h>

#include <stdlib.h>
#include <string.h>

int waydict_defaults_copy_string(const char *key, char **value, bool *found) {
  if (key == NULL || value == NULL || found == NULL) return -1;
  *value = NULL;
  *found = false;
  @try {
    @autoreleasepool {
      NSString *nativeKey = [NSString stringWithUTF8String:key];
      if (nativeKey == nil) return -1;
      id object = [[NSUserDefaults standardUserDefaults] objectForKey:nativeKey];
      if (object == nil) return 0;
      if (![object isKindOfClass:NSString.class]) return -2;
      const char *text = [(NSString *)object UTF8String];
      if (text == NULL) return -2;
      *value = strdup(text);
      if (*value == NULL) return -3;
      *found = true;
      return 0;
    }
  } @catch (__unused NSException *exception) {
    return -4;
  }
}

int waydict_defaults_set_string(const char *key, const char *value) {
  if (key == NULL || value == NULL) return -1;
  @try {
    @autoreleasepool {
      NSString *nativeKey = [NSString stringWithUTF8String:key];
      NSString *nativeValue = [NSString stringWithUTF8String:value];
      if (nativeKey == nil || nativeValue == nil) return -1;
      [[NSUserDefaults standardUserDefaults] setObject:nativeValue forKey:nativeKey];
      return 0;
    }
  } @catch (__unused NSException *exception) {
    return -4;
  }
}

int waydict_defaults_delete(const char *key) {
  if (key == NULL) return -1;
  @try {
    @autoreleasepool {
      NSString *nativeKey = [NSString stringWithUTF8String:key];
      if (nativeKey == nil) return -1;
      [[NSUserDefaults standardUserDefaults] removeObjectForKey:nativeKey];
      return 0;
    }
  } @catch (__unused NSException *exception) {
    return -4;
  }
}

void waydict_defaults_free(void *value) {
  free(value);
}
