//go:build darwin && cgo

package runtimeimage

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

// SecCodeCopySigningInformation alone reads disk and is NOT running identity.
// Dynamic validation checks the host's loaded CodeDirectory against that disk
// object first. The static object is retained throughout both validation and
// extraction; a replaced pathname before acquisition fails closed.
static OSStatus verified_image_digest(const char *path, char *out) {
 SecCodeRef running = NULL;
 SecStaticCodeRef code = NULL;
 CFURLRef url = NULL;
 CFDictionaryRef info = NULL;
 OSStatus result;
 const SecCSFlags flags = kSecCSStrictValidate | kSecCSNoNetworkAccess;
 if (path == NULL) {
  result = SecCodeCopySelf(kSecCSDefaultFlags, &running);
  if (result != errSecSuccess) goto done;
  result = SecCodeCheckValidity(running, flags, NULL);
  if (result != errSecSuccess) goto done;
  result = SecCodeCopyStaticCode(running, kSecCSDefaultFlags, &code);
 } else {
  url = CFURLCreateFromFileSystemRepresentation(NULL, (const UInt8 *)path, strlen(path), false);
  if (url == NULL) { result = errSecParam; goto done; }
  result = SecStaticCodeCreateWithPath(url, kSecCSDefaultFlags, &code);
 }
 if (result != errSecSuccess) goto done;
 result = SecStaticCodeCheckValidity(code, flags, NULL);
 if (result != errSecSuccess) goto done;
 result = SecCodeCopySigningInformation(code, kSecCSDefaultFlags, &info);
 if (result != errSecSuccess) goto done;
 CFDataRef digest = (CFDataRef)CFDictionaryGetValue(info, kSecCodeInfoUnique);
 if (digest == NULL || CFGetTypeID(digest) != CFDataGetTypeID() ||
     CFDataGetLength(digest) < 20 || CFDataGetLength(digest) > 64) {
  result = errSecCSUnsigned;
  goto done;
 }
 const UInt8 *bytes = CFDataGetBytePtr(digest);
 const char *hex = "0123456789abcdef";
 for (CFIndex i = 0; i < CFDataGetLength(digest); i++) {
  out[2*i] = hex[bytes[i] >> 4];
  out[2*i+1] = hex[bytes[i] & 15];
 }
 out[2*CFDataGetLength(digest)] = 0;
done:
 if (info) CFRelease(info);
 if (code) CFRelease(code);
 if (running) CFRelease(running);
 if (url) CFRelease(url);
 return result;
}
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"
)

func runningDigest() (string, string, error) {
	return darwinDigest(nil)
}

func candidateDigest(ctx context.Context, path string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	return darwinDigest(name)
}

func darwinDigest(path *C.char) (string, string, error) {
	var digest [129]C.char
	status := C.verified_image_digest(path, &digest[0])
	if status != C.errSecSuccess {
		return "", "", fmt.Errorf("darwin image validation failed (%d)", int(status))
	}
	return "darwin-cdhash", C.GoString(&digest[0]), nil
}
