//go:build darwin && cgo

package groups

/*
#cgo CFLAGS: -x objective-c -mmacosx-version-min=12.0 -fobjc-arc
#cgo LDFLAGS: -framework AVFoundation -framework CoreMedia -framework CoreVideo -framework Foundation -framework CoreGraphics
#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>
#import <CoreMedia/CoreMedia.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>

// ScreenCaptureKit is loaded at run time, not linked: linking it would stop
// the whole aos binary from loading on macOS 12.0-12.2. Its classes are
// reached by name and its one constant through dlsym.
static BOOL aosLoadSCK(void) {
	static BOOL loaded = NO;
	if (!loaded) loaded = dlopen("/System/Library/Frameworks/ScreenCaptureKit.framework/ScreenCaptureKit", RTLD_LAZY) != NULL;
	return loaded && NSClassFromString(@"SCShareableContent") != nil;
}
static NSString *aosFrameStatusKey(void) {
	NSString *__unsafe_unretained *p = (NSString *__unsafe_unretained *)dlsym(RTLD_DEFAULT, "SCStreamFrameInfoStatus");
	return p ? *p : @"SCStreamFrameInfoStatus";
}
#define SCK(name) ((Class)NSClassFromString(@#name))

// One window, one SCStream, one AVAssetWriter. ScreenCaptureKit captures the
// window's own pixels, so other windows on top never appear in the video.

API_AVAILABLE(macos(12.3))
@interface AOSWinRec : NSObject <SCStreamOutput, SCStreamDelegate>
@property (strong) SCStream *stream;
@property (strong) AVAssetWriter *writer;
@property (strong) AVAssetWriterInput *input;
@property (strong) dispatch_queue_t queue;
@property (assign) BOOL started;
@property (assign) long frames;
@property (strong) NSString *failure;
@end

@implementation AOSWinRec
- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)sb ofType:(SCStreamOutputType)type {
	if (type != SCStreamOutputTypeScreen || !CMSampleBufferIsValid(sb)) return;
	CFArrayRef atts = CMSampleBufferGetSampleAttachmentsArray(sb, false);
	if (!atts || CFArrayGetCount(atts) == 0) return;
	NSDictionary *info = (__bridge NSDictionary *)CFArrayGetValueAtIndex(atts, 0);
	NSNumber *status = info[aosFrameStatusKey()];
	if (status == nil || status.integerValue != SCFrameStatusComplete) return; // idle/blank frames carry no image
	if (!self.started) {
		if (![self.writer startWriting]) { self.failure = self.writer.error.localizedDescription; return; }
		[self.writer startSessionAtSourceTime:CMSampleBufferGetPresentationTimeStamp(sb)];
		self.started = YES;
	}
	if (self.input.readyForMoreMediaData && [self.input appendSampleBuffer:sb]) self.frames++;
}
- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
	self.failure = error.localizedDescription;
}
@end

static NSMutableArray *aosRecs;

static void aosErr(char *buf, int len, NSString *msg) {
	if (buf && len > 0) { strncpy(buf, msg.UTF8String ? msg.UTF8String : "error", len - 1); buf[len - 1] = 0; }
}

// aos_sck_windows fills sizes for the given window ids: w,h in points, or 0
// when the window is not shareable. Returns 0, or -1 with errbuf set.
static int aos_sck_windows(const unsigned int *ids, int n, double *sizes, char *errbuf, int errlen) {
	if (!aosLoadSCK()) { aosErr(errbuf, errlen, @"window capture needs ScreenCaptureKit (macOS 12.3 or later)"); return -1; }
	if (@available(macOS 12.3, *)) {
		__block SCShareableContent *content = nil;
		__block NSError *err = nil;
		dispatch_semaphore_t sem = dispatch_semaphore_create(0);
		[(id)SCK(SCShareableContent) getShareableContentExcludingDesktopWindows:YES onScreenWindowsOnly:NO
			completionHandler:^(SCShareableContent *c, NSError *e) { content = c; err = e; dispatch_semaphore_signal(sem); }];
		if (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC)) != 0) { aosErr(errbuf, errlen, @"timed out asking ScreenCaptureKit for windows"); return -1; }
		if (err || !content) { aosErr(errbuf, errlen, err ? err.localizedDescription : @"no shareable content (Screen Recording permission?)"); return -1; }
		for (int i = 0; i < n; i++) {
			sizes[2*i] = 0; sizes[2*i+1] = 0;
			for (SCWindow *w in content.windows) {
				if (w.windowID == ids[i]) { sizes[2*i] = w.frame.size.width; sizes[2*i+1] = w.frame.size.height; break; }
			}
		}
		return 0;
	}
	aosErr(errbuf, errlen, @"window capture needs macOS 12.3 or later");
	return -1;
}

// aos_sck_start starts one stream per window, writing outs[i]. Returns 0 or -1.
static int aos_sck_start(const unsigned int *ids, int n, const char **outs, int fps, double scale, char *errbuf, int errlen) {
	if (!aosLoadSCK()) { aosErr(errbuf, errlen, @"window capture needs ScreenCaptureKit (macOS 12.3 or later)"); return -1; }
	if (@available(macOS 12.3, *)) {
		__block SCShareableContent *content = nil;
		__block NSError *err = nil;
		dispatch_semaphore_t sem = dispatch_semaphore_create(0);
		[(id)SCK(SCShareableContent) getShareableContentExcludingDesktopWindows:YES onScreenWindowsOnly:NO
			completionHandler:^(SCShareableContent *c, NSError *e) { content = c; err = e; dispatch_semaphore_signal(sem); }];
		if (dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC)) != 0) { aosErr(errbuf, errlen, @"timed out asking ScreenCaptureKit for windows"); return -1; }
		if (err || !content) { aosErr(errbuf, errlen, err ? err.localizedDescription : @"no shareable content (Screen Recording permission?)"); return -1; }
		aosRecs = [NSMutableArray array];
		for (int i = 0; i < n; i++) {
			SCWindow *win = nil;
			for (SCWindow *w in content.windows) if (w.windowID == ids[i]) { win = w; break; }
			if (!win) { aosErr(errbuf, errlen, [NSString stringWithFormat:@"window %u is not shareable", ids[i]]); return -1; }
			int width = ((int)(win.frame.size.width * scale)) & ~1, height = ((int)(win.frame.size.height * scale)) & ~1;
			if (width < 16 || height < 16) { aosErr(errbuf, errlen, @"window is too small to record"); return -1; }

			AOSWinRec *rec = [AOSWinRec new];
			NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:outs[i]]];
			[[NSFileManager defaultManager] removeItemAtURL:url error:nil];
			NSError *werr = nil;
			rec.writer = [AVAssetWriter assetWriterWithURL:url fileType:AVFileTypeMPEG4 error:&werr];
			if (!rec.writer) { aosErr(errbuf, errlen, werr.localizedDescription); return -1; }
			rec.input = [AVAssetWriterInput assetWriterInputWithMediaType:AVMediaTypeVideo outputSettings:@{
				AVVideoCodecKey: AVVideoCodecTypeH264, AVVideoWidthKey: @(width), AVVideoHeightKey: @(height)}];
			rec.input.expectsMediaDataInRealTime = YES;
			[rec.writer addInput:rec.input];

			SCContentFilter *filter = [[SCK(SCContentFilter) alloc] initWithDesktopIndependentWindow:win];
			SCStreamConfiguration *cfg = [SCK(SCStreamConfiguration) new];
			cfg.width = width; cfg.height = height;
			cfg.minimumFrameInterval = CMTimeMake(1, fps);
			cfg.showsCursor = YES;
			cfg.pixelFormat = kCVPixelFormatType_32BGRA;
			rec.queue = dispatch_queue_create("aos.winrec", DISPATCH_QUEUE_SERIAL);
			rec.stream = [[SCK(SCStream) alloc] initWithFilter:filter configuration:cfg delegate:rec];
			NSError *aerr = nil;
			if (![rec.stream addStreamOutput:rec type:SCStreamOutputTypeScreen sampleHandlerQueue:rec.queue error:&aerr]) {
				aosErr(errbuf, errlen, aerr.localizedDescription); return -1;
			}
			__block NSError *serr = nil;
			dispatch_semaphore_t started = dispatch_semaphore_create(0);
			[rec.stream startCaptureWithCompletionHandler:^(NSError *e) { serr = e; dispatch_semaphore_signal(started); }];
			dispatch_semaphore_wait(started, dispatch_time(DISPATCH_TIME_NOW, 10 * NSEC_PER_SEC));
			if (serr) { aosErr(errbuf, errlen, serr.localizedDescription); return -1; }
			[aosRecs addObject:rec];
		}
		return 0;
	}
	aosErr(errbuf, errlen, @"window capture needs macOS 12.3 or later");
	return -1;
}

// aos_sck_failure reports the first stream failure so far, or "".
static void aos_sck_failure(char *errbuf, int errlen) {
	errbuf[0] = 0;
	if (@available(macOS 12.3, *)) {
		for (AOSWinRec *rec in aosRecs) if (rec.failure) { aosErr(errbuf, errlen, rec.failure); return; }
	}
}

// aos_sck_stop stops every stream and finishes every file. Returns frames written.
static long aos_sck_stop(char *errbuf, int errlen) {
	long frames = 0;
	errbuf[0] = 0;
	if (@available(macOS 12.3, *)) {
		for (AOSWinRec *rec in aosRecs) {
			dispatch_semaphore_t s1 = dispatch_semaphore_create(0);
			[rec.stream stopCaptureWithCompletionHandler:^(NSError *e) { dispatch_semaphore_signal(s1); }];
			dispatch_semaphore_wait(s1, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC));
			dispatch_sync(rec.queue, ^{}); // drain queued frames
			if (!rec.started) { aosErr(errbuf, errlen, rec.failure ? rec.failure : @"no frames were captured"); continue; }
			[rec.input markAsFinished];
			dispatch_semaphore_t s2 = dispatch_semaphore_create(0);
			[rec.writer finishWritingWithCompletionHandler:^{ dispatch_semaphore_signal(s2); }];
			dispatch_semaphore_wait(s2, dispatch_time(DISPATCH_TIME_NOW, 20 * NSEC_PER_SEC));
			if (rec.writer.status != AVAssetWriterStatusCompleted) aosErr(errbuf, errlen, rec.writer.error.localizedDescription ?: @"writing the video failed");
			frames += rec.frames;
		}
	}
	aosRecs = nil;
	return frames;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

const winRecSupported = true

// sckWindowSizes reports each window's size in points ({0,0} = not shareable),
// without capturing anything.
func sckWindowSizes(ids []uint32) ([][2]float64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	cids := make([]C.uint, len(ids))
	for i, id := range ids {
		cids[i] = C.uint(id)
	}
	sizes := make([]C.double, 2*len(ids))
	var buf [512]C.char
	if C.aos_sck_windows(&cids[0], C.int(len(ids)), &sizes[0], &buf[0], C.int(len(buf))) != 0 {
		return nil, errors.New(C.GoString(&buf[0]))
	}
	out := make([][2]float64, len(ids))
	for i := range ids {
		out[i] = [2]float64{float64(sizes[2*i]), float64(sizes[2*i+1])}
	}
	return out, nil
}

// sckStart begins recording each window into its file.
func sckStart(ids []uint32, outs []string, fps int, scale float64) error {
	cids := make([]C.uint, len(ids))
	couts := make([]*C.char, len(outs))
	for i := range ids {
		cids[i] = C.uint(ids[i])
		couts[i] = C.CString(outs[i])
		defer C.free(unsafe.Pointer(couts[i]))
	}
	var buf [512]C.char
	if C.aos_sck_start(&cids[0], C.int(len(ids)), &couts[0], C.int(fps), C.double(scale), &buf[0], C.int(len(buf))) != 0 {
		return errors.New(C.GoString(&buf[0]))
	}
	return nil
}

// sckFailure is a stream failure reported since the start, or nil.
func sckFailure() error {
	var buf [512]C.char
	C.aos_sck_failure(&buf[0], C.int(len(buf)))
	if s := C.GoString(&buf[0]); s != "" {
		return errors.New(s)
	}
	return nil
}

// sckStop finishes every file and returns how many frames were written.
func sckStop() (int, error) {
	var buf [512]C.char
	n := C.aos_sck_stop(&buf[0], C.int(len(buf)))
	if s := C.GoString(&buf[0]); s != "" {
		return int(n), errors.New(s)
	}
	return int(n), nil
}
