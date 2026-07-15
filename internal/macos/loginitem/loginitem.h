#ifndef WAYDICT_LOGINITEM_H
#define WAYDICT_LOGINITEM_H

int waydict_loginitem_status(void);
int waydict_loginitem_set_enabled(int enabled, char **error_message);
void waydict_loginitem_free_error(char *error_message);

#endif
