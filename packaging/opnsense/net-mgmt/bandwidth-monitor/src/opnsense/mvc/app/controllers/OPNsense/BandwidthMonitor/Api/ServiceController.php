<?php

namespace OPNsense\BandwidthMonitor\Api;

use OPNsense\Base\ApiMutableServiceControllerBase;

class ServiceController extends ApiMutableServiceControllerBase
{
    protected static $internalServiceClass = '\OPNsense\BandwidthMonitor\General';
    protected static $internalServiceEnabled = 'enabled';
    protected static $internalServiceTemplate = 'OPNsense/BandwidthMonitor';
    protected static $internalServiceName = 'bandwidthmonitor';
}
