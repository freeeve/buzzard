//go:build darwin && cgo

package trash

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

static int buzzard_trash(const char *path, char **outPath) {
	@autoreleasepool {
		NSString *p = [[NSFileManager defaultManager]
		    stringWithFileSystemRepresentation:path length:strlen(path)];
		NSURL *url = [NSURL fileURLWithPath:p];
		NSURL *result = nil;
		NSError *err = nil;
		BOOL ok = [[NSFileManager defaultManager] trashItemAtURL:url
		                                resultingItemURL:&result
		                                           error:&err];
		if (!ok) {
			return err != nil ? (int)err.code : 1;
		}
		if (outPath != NULL && result != nil) {
			*outPath = strdup([result fileSystemRepresentation]);
		}
		return 0;
	}
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Put moves path into the user's Trash through NSFileManager, preserving
// Finder's put-back metadata, and returns where the item landed.
func Put(path string) (string, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var out *C.char
	if rc := C.buzzard_trash(cpath, &out); rc != 0 {
		return "", fmt.Errorf("trash %s: NSFileManager error %d", path, int(rc))
	}
	if out == nil {
		return "", fmt.Errorf("trash %s: no resulting location reported", path)
	}
	trashed := C.GoString(out)
	C.free(unsafe.Pointer(out))
	return trashed, nil
}
