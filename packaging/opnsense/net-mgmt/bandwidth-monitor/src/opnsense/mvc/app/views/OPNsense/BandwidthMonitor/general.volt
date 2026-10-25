<script>
    $(document).ready(function() {
        mapDataToFormUI({'frm_general_settings': "/api/bandwidthmonitor/general/get"}).done(function() {
            $('.selectpicker').selectpicker('refresh');
        });
        updateServiceControlUI('bandwidthmonitor');

        $("#saveAct").SimpleActionButton({
            onPreAction: function() {
                const dfObj = new $.Deferred();
                saveFormToEndpoint("/api/bandwidthmonitor/general/set", 'frm_general_settings', function() {
                    dfObj.resolve();
                });
                return dfObj;
            },
            onAction: function() {
                updateServiceControlUI('bandwidthmonitor');
            }
        });
    });
</script>

<div class="content-box" style="padding-bottom: 1.5em;">
    {{ partial("layout_partials/base_form", ['fields':generalForm,'id':'frm_general_settings']) }}
    <div class="col-md-12">
        <hr />
        <button class="btn btn-primary" id="saveAct"
                data-endpoint="/api/bandwidthmonitor/service/reconfigure"
                data-label="{{ lang._('Apply') }}"
                data-error-title="{{ lang._('Error reconfiguring Bandwidth Monitor') }}"
                type="button"></button>
    </div>
</div>
