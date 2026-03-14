#include <unistd.h>
#include <stdlib.h>
int main(int argc, char *argv[]) {
    setuid(0);
    setgid(0);
    char *env[] = {"HOME=/root", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", NULL};
    execve("/usr/local/var/lib/asterisk/agi-bin/german_trainer", argv, env);
    return 1;
}
