// NWPathMonitor (Network.framework) for `aos watch network`: calls back
// into Go whenever the system's network path changes. The callback carries
// no state; Go re-reads the network itself.

#include <Network/Network.h>
#include <dispatch/dispatch.h>

extern void aosNetPathChanged(void);

static nw_path_monitor_t aos_net_monitor;

void aosNetStart(void) {
	if (aos_net_monitor != NULL) {
		return;
	}
	aos_net_monitor = nw_path_monitor_create();
	nw_path_monitor_set_queue(aos_net_monitor, dispatch_get_global_queue(QOS_CLASS_UTILITY, 0));
	nw_path_monitor_set_update_handler(aos_net_monitor, ^(nw_path_t path) {
		(void)path;
		aosNetPathChanged();
	});
	nw_path_monitor_start(aos_net_monitor);
}

void aosNetStop(void) {
	if (aos_net_monitor != NULL) {
		nw_path_monitor_cancel(aos_net_monitor);
		aos_net_monitor = NULL;
	}
}
