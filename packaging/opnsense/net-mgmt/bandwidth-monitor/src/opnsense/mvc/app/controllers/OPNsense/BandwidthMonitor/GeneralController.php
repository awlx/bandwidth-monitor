<?php

namespace OPNsense\BandwidthMonitor;

use OPNsense\Base\IndexController;

class GeneralController extends IndexController
{
    public function indexAction()
    {
        $this->view->pick('OPNsense/BandwidthMonitor/general');
        $this->view->generalForm = $this->getForm("general");
    }
}
