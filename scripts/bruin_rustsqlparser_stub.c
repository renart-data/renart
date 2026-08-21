#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static char *copy_response(const char *response) {
    size_t length = strlen(response) + 1;
    char *result = malloc(length);
    if (result != NULL) {
        memcpy(result, response, length);
    }
    return result;
}

char *bruin_rustsqlparser_get_tables(const char *query, const char *dialect) {
    (void)query;
    (void)dialect;
    return copy_response("{\"tables\":[],\"error\":\"Bruin RustSQLParser is disabled; Renart uses native Golyglot\"}");
}

char *bruin_rustsqlparser_rename_tables(const char *query, const char *dialect, const char *table_mapping_json) {
    (void)query;
    (void)dialect;
    (void)table_mapping_json;
    return copy_response("{\"query\":\"\",\"error\":\"Bruin RustSQLParser is disabled; Renart uses native Golyglot\"}");
}

char *bruin_rustsqlparser_add_limit(const char *query, int64_t limit, const char *dialect) {
    (void)query;
    (void)limit;
    (void)dialect;
    return copy_response("{\"query\":\"\",\"error\":\"Bruin RustSQLParser is disabled; Renart uses native Golyglot\"}");
}

char *bruin_rustsqlparser_is_single_select(const char *query, const char *dialect) {
    (void)query;
    (void)dialect;
    return copy_response("{\"is_single_select\":false,\"error\":\"Bruin RustSQLParser is disabled; Renart uses native Golyglot\"}");
}

char *bruin_rustsqlparser_column_lineage(const char *query, const char *dialect, const char *schema_json) {
    (void)query;
    (void)dialect;
    (void)schema_json;
    /* Bruin's lineage wrapper has no top-level error field, so make decoding
       fail instead of returning a plausible empty lineage result. */
    return copy_response("{\"columns\":\"Bruin RustSQLParser is disabled; Renart uses native Golyglot\"}");
}

char *bruin_rustsqlparser_hoist_declares(const char *query, const char *dialect) {
    (void)query;
    (void)dialect;
    return copy_response("{\"query\":\"\",\"error\":\"Bruin RustSQLParser is disabled; Renart uses native Golyglot\"}");
}

char *bruin_rustsqlparser_hoist_declares_list(const char *queries_json, const char *dialect) {
    (void)queries_json;
    (void)dialect;
    return copy_response("{\"queries\":[],\"error\":\"Bruin RustSQLParser is disabled; Renart uses native Golyglot\"}");
}

void bruin_rustsqlparser_free_string(char *value) {
    free(value);
}
