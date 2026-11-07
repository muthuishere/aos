package groups

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <stdlib.h>

// aosSharedFileList resolves every bookmark in a LaunchServices shared file
// list (.sfl2/.sfl3/.sfl4: an NSKeyedArchiver dictionary whose "items" each
// hold a "Bookmark"), newest first, one path per line.
static char *aosSharedFileList(const char *path) {
	@autoreleasepool {
		NSData *data = [NSData dataWithContentsOfFile:[NSString stringWithUTF8String:path]];
		if (!data) return NULL;
		NSKeyedUnarchiver *u = [[NSKeyedUnarchiver alloc] initForReadingFromData:data error:NULL];
		if (!u) return NULL;
		u.requiresSecureCoding = NO;
		id root = [u decodeObjectForKey:NSKeyedArchiveRootObjectKey];
		[u finishDecoding];
		if (![root isKindOfClass:[NSDictionary class]]) return NULL;
		NSMutableString *out = [NSMutableString string];
		for (id item in (NSArray *)((NSDictionary *)root)[@"items"]) {
			if (![item isKindOfClass:[NSDictionary class]]) continue;
			NSData *bm = ((NSDictionary *)item)[@"Bookmark"];
			if (![bm isKindOfClass:[NSData class]]) continue;
			NSURL *url = [NSURL URLByResolvingBookmarkData:bm
				options:NSURLBookmarkResolutionWithoutUI | NSURLBookmarkResolutionWithoutMounting
				relativeToURL:nil bookmarkDataIsStale:NULL error:NULL];
			if (url.isFileURL && url.path) [out appendFormat:@"%@\n", url.path];
		}
		return strdup(out.UTF8String);
	}
}
*/
import "C"

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

func sharedFileListDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "com.apple.sharedfilelist")
}

func readSharedFileList(path string) []string {
	cs := C.CString(path)
	defer C.free(unsafe.Pointer(cs))
	out := C.aosSharedFileList(cs)
	if out == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(out))
	var paths []string
	for _, p := range strings.Split(C.GoString(out), "\n") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// listRecent reads the system-wide Recent Documents list, or with an app
// filter the per-app lists (named by bundle id, e.g. com.apple.TextEdit).
func listRecent(app string) ([]recentFile, error) {
	dir := sharedFileListDir()
	if app == "" {
		matches, _ := filepath.Glob(filepath.Join(dir, "com.apple.LSSharedFileList.RecentDocuments.sfl*"))
		return pathsToRecent(matches, ""), nil
	}
	lists, _ := filepath.Glob(filepath.Join(dir, "com.apple.LSSharedFileList.ApplicationRecentDocuments", "*.sfl*"))
	var picked []string
	for _, l := range lists {
		if recentAppMatches(app, strings.TrimSuffix(filepath.Base(l), filepath.Ext(l))) {
			picked = append(picked, l)
		}
	}
	files := pathsToRecent(picked, "app")
	if len(picked) > 1 {
		// Lists from several apps have no common clock; the file's own
		// modification time is the best shared order.
		for i := range files {
			if st, err := os.Stat(files[i].Path); err == nil {
				files[i].when = st.ModTime()
			}
		}
		newestFirst(files)
	}
	return files, nil
}

func pathsToRecent(lists []string, withApp string) []recentFile {
	var out []recentFile
	for _, l := range lists {
		bundle := strings.TrimSuffix(filepath.Base(l), filepath.Ext(l))
		for _, p := range readSharedFileList(l) {
			f := recentFile{Path: p}
			if withApp != "" {
				f.App = bundle
			}
			out = append(out, f)
		}
	}
	return out
}
