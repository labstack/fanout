WITH resources AS MATERIALIZED (
 SELECT namespace,service,resource,min(start_time) AS seen FROM spans WHERE $__window(start_time) GROUP BY namespace,service,resource
 UNION ALL SELECT namespace,service,resource,min(time) AS seen FROM logs WHERE $__window(time) GROUP BY namespace,service,resource
 UNION ALL SELECT namespace,service,resource,min(time) AS seen FROM metrics WHERE $__window(time) GROUP BY namespace,service,resource)
 SELECT namespace,service,resource['service.version']::VARCHAR AS version,resource['process.runtime.name']::VARCHAR AS runtime_name,resource['process.runtime.version']::VARCHAR AS runtime_version,resource['telemetry.sdk.language']::VARCHAR AS sdk_language,resource['telemetry.sdk.version']::VARCHAR AS sdk_version,seen FROM resources
