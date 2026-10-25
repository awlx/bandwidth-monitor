<?php

function bandwidthmonitor_services()
{
    $services = array();
    $mdl = new \OPNsense\BandwidthMonitor\General();

    if ((string)$mdl->enabled != '1') {
        return $services;
    }

    $services[] = array(
        'description' => gettext('Bandwidth Monitor'),
        'configd' => array(
            'restart' => array('bandwidthmonitor restart'),
            'start' => array('bandwidthmonitor start'),
            'stop' => array('bandwidthmonitor stop'),
        ),
        'name' => 'bandwidthmonitor',
        'pidfile' => '/var/run/bandwidth_monitor.pid'
    );

    return $services;
}
