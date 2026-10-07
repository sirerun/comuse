//go:build darwin && cgo

package writer

/*
#cgo LDFLAGS: -framework Security -framework CoreGraphics -framework CoreFoundation
#include <Security/AuthSession.h>
#include <CoreGraphics/CGSession.h>
#include <CoreFoundation/CoreFoundation.h>
#include <unistd.h>

static int comuse_desktop_identity(uint32_t *uid_out, uint32_t *session_out) {
	SecuritySessionId session = 0;
	SessionAttributeBits attributes = 0;
	if (SessionGetInfo(callerSecuritySession, &session, &attributes) != 0 ||
	    session == noSecuritySession || !(attributes & sessionHasGraphicAccess)) return -1;
	CFDictionaryRef dictionary = CGSessionCopyCurrentDictionary();
	if (!dictionary) return -1;
	CFTypeRef uid_value = CFDictionaryGetValue(dictionary, kCGSessionUserIDKey);
	CFTypeRef on_console_value = CFDictionaryGetValue(dictionary, kCGSessionOnConsoleKey);
	CFTypeRef login_done_value = CFDictionaryGetValue(dictionary, kCGSessionLoginDoneKey);
	int ok = 0;
	int32_t quartz_uid = -1;
	if (uid_value && CFGetTypeID(uid_value) == CFNumberGetTypeID() &&
	    on_console_value && CFGetTypeID(on_console_value) == CFBooleanGetTypeID() &&
	    login_done_value && CFGetTypeID(login_done_value) == CFBooleanGetTypeID() &&
	    CFBooleanGetValue((CFBooleanRef)on_console_value) &&
	    CFBooleanGetValue((CFBooleanRef)login_done_value) &&
	    CFNumberGetValue((CFNumberRef)uid_value, kCFNumberSInt32Type, &quartz_uid) &&
	    quartz_uid >= 0 && (uint32_t)quartz_uid == (uint32_t)getuid()) {
		*uid_out = (uint32_t)getuid();
		*session_out = (uint32_t)session;
		ok = 1;
	}
	CFRelease(dictionary);
	return ok ? 0 : -1;
}
*/
import "C"

import (
	"errors"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

func resolveDesktopIdentity() (desktopIdentity, error) {
	var uid, session C.uint32_t
	if C.comuse_desktop_identity(&uid, &session) != 0 {
		return desktopIdentity{}, ErrDesktopIdentityUnavailable
	}
	actualUID := uint32(os.Getuid())
	if uint32(uid) != actualUID || uint32(session) == 0 {
		return desktopIdentity{}, ErrDesktopIdentityUnavailable
	}
	account, err := user.LookupId(strconv.FormatUint(uint64(actualUID), 10))
	if err != nil || account.Uid != strconv.FormatUint(uint64(actualUID), 10) {
		return desktopIdentity{}, ErrDesktopIdentityUnavailable
	}
	return desktopIdentity{uid: actualUID, sessionID: uint32(session)}, nil
}

func verifyUIDOwner(info os.FileInfo, uid uint32) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint32(stat.Uid) != uid {
		return errors.New("canonical desktop state is not owned by the verified user")
	}
	return nil
}
