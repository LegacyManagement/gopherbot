#!/bin/bash -e

# cbot.sh - Script to simplify running Gopherbot containers

IMAGE_NAME="ghcr.io/lnxjedi/gopherbot"
IMAGE_TAG="latest"

usage() {
    cat <<EOF
Usage: ./cbot.sh <command> (options...) (arguments...)
Use './cbot.sh <command> -h' for help on a given command.
Commands:
- preview: launch the preview for a terminal interface to the default robot
- profile <robotname> <fullname> <email>: generate a robot development profile
- pull: pull the latest docker images
- start <robot.env>: start a development (or production) robot container
- stop <robot.env>: stop a robot container
- remove <robot.env>: stop and remove a robot container
- terminal <robot.env>: shell in to the robot's container
- list: generate a list of robot containers
EOF
}

if [ $# -lt 1 ]
then
    usage
    exit 0
fi

COMMAND="$1"
shift

get_access() {
    echo "http://localhost:7777/?workspace=/home/bot/gopherbot.code-workspace&tkn=$RANDOM_TOKEN"
}

show_access() {
    local ENV_TYPE="dev"
    if [ "$1" == "-p" ]
    then
        ENV_TYPE="preview"
        shift
    fi
    GENERATED=$(get_access)
    local ACCESS_URL=${1:-$GENERATED}
    echo "Access your $ENV_TYPE environment at: $ACCESS_URL"
}

check_profile() {
    if [ ! "$GOPHER_PROFILE" ]
    then
        echo "Missing profile argument" 
        exit 1
    fi

    if [ ! -e "$GOPHER_PROFILE" ]
    then
        echo "Profile file not found: $GOPHER_PROFILE"
        exit 1
    fi
}

read_profile() {
    for CFG_VAR in CONTAINERNAME SSH_KEY_PATH
    do
        RAW=$(grep "^#|$CFG_VAR" $GOPHER_PROFILE)
        echo "${CFG_VAR}=${RAW#*=}"
    done
}

wait_for_container() {
    # Give it a minute to start running
    for TRY in {1..60}
    do
        if [ "`docker inspect -f {{.State.Running}} $CONTAINERNAME`"=="true" ]
        then
            SUCCESS="true"
            break
        fi
        sleep 1
    done
}

copy_ssh() {
    if [ "$SSH_KEY_PATH" ]
    then
        echo "Copying $SSH_KEY_PATH to $CONTAINERNAME:/home/bot/.ssh/id_ssh ..."
        docker cp "$SSH_KEY_PATH" $CONTAINERNAME:/home/bot/.ssh/id_ssh
        docker exec -it -u root $CONTAINERNAME /bin/bash -c "chown bot:bot /home/bot/.ssh/id_ssh; chmod 0600 /home/bot/.ssh/id_ssh"
    fi
}

case $COMMAND in
profile )
    while getopts ":hk:" OPT; do
        case $OPT in
        h ) cat <<"EOF"
Generate a profile for working with a gopherbot robot container:
./cbot.sh profile (-k path/to/ssh/private/key) <container-name> "<full name>" <email>
 -k (path) - Load an ssh private key when using this profile

Example:
$ ./cbot.sh profile -k ~/.ssh/id_rsa bishop "David Parsley" parsley@linuxjedi.org | tee bishop.env
## Lines starting with #| are used by the cbot.sh script
GIT_AUTHOR_NAME="David Parsley"
GIT_AUTHOR_EMAIL=parsley@linuxjedi.org
GIT_COMMITTER_NAME="David Parsley"
GIT_COMMITTER_EMAIL=parsley@linuxjedi.org
#|CONTAINERNAME=bishop
#|SSH_KEY_PATH=/home/david/.ssh/id_rsa
EOF
            exit 0
            ;;
        k )
            SSH_KEY_PATH="$OPTARG"
            ;;
        \?)
            [ "$OPT" != "h" ] && echo "Invalid option: $OPTARG"
            usage
            exit 0
            ;;
        esac
    done
    shift $((OPTIND -1))
    if [ $# -ne 3 ]
    then
        echo "Wrong number of arguments"
        usage
        exit 1
    fi
    CONTAINERNAME="$1"
    GIT_USER="$2"
    GIT_EMAIL="$3"
    cat <<EOF
## Lines starting with #| are used by the cbot.sh script
GIT_AUTHOR_NAME="${GIT_USER}"
GIT_AUTHOR_EMAIL=${GIT_EMAIL}
GIT_COMMITTER_NAME="${GIT_USER}"
GIT_COMMITTER_EMAIL=${GIT_EMAIL}
#|CONTAINERNAME=${CONTAINERNAME}
EOF
    if [ "$SSH_KEY_PATH" ]
    then
        echo "#|SSH_KEY_PATH=${SSH_KEY_PATH}"
    fi
    exit 0
    ;;
list | ls )
    while getopts ":h" OPT; do
        case $OPT in
        h ) cat <<"EOF"
List all robot containers:
./cbot.sh list

Example:
$ ./cbot.sh list
CONTAINER ID   STATUS             NAMES        environment         access
0f50a4ce6b2a   Up 37 seconds      bishop-dev   robot/development   http://localhost:7777/?workspace=/home/bot/gopherbot.code-workspace&tkn=XXXXXXX
1c470fd80c31   Up About an hour   clu          robot/production 
EOF
            exit 0
            ;;
        \?)
            [ "$OPT" != "h" ] && echo "Invalid option: $OPTARG"
            usage
            exit 0
            ;;
        esac
    done
    docker ps -a --filter "label=type=gopherbot/robot" --format "table {{.ID}}\t{{.Status}}\t{{.Names}}\t{{.Label \"environment\"}}\t{{.Label \"access\"}}"
    ;;
remove | rm )
    while getopts ":hp" OPT; do
        case $OPT in
        h ) cat <<"EOF"
Stop and remove a container:
./cbot.sh remove (path/to/profile)
 -p - remove a production robot

Example:
$ ./cbot.sh remove bishop.env
EOF
            exit 0
            ;;
        p )
            PROD="true"
            ;;
        \?)
            [ "$OPT" != "h" ] && echo "Invalid option: $OPTARG"
            usage
            exit 0
            ;;
        esac
    done
    shift $((OPTIND -1))
    GOPHER_PROFILE=$1
    check_profile
    eval `read_profile`
    if [ ! "$PROD" ]
    then
        CONTAINERNAME="$CONTAINERNAME-dev"
    fi
    docker stop $CONTAINERNAME >/dev/null && docker rm $CONTAINERNAME >/dev/null
    echo "Removed"
    exit 0
    ;;
stop )
    while getopts ":hp" OPT; do
        case $OPT in
        h ) cat <<"EOF"
Stop a robot container:
./cbot.sh stop (-p) (path/to/profile)
 -p - stop a production robot

Example:
$ ./cbot.sh stop bishop.env
EOF
            exit 0
            ;;
        p )
            PROD="true"
            ;;
        \?)
            [ "$OPT" != "h" ] && echo "Invalid option: $OPTARG"
            usage
            exit 0
            ;;
        esac
    done
    shift $((OPTIND -1))
    GOPHER_PROFILE=$1
    check_profile
    eval `read_profile`
    if [ ! "$PROD" ]
    then
        CONTAINERNAME="$CONTAINERNAME-dev"
    fi
    docker stop $CONTAINERNAME >/dev/null
    echo "Stopped"
    exit 0
    ;;
term | terminal )
    while getopts ":hrp" OPT; do
        case $OPT in
        h ) cat <<"EOF"
Shell in to a robot container:
./cbot.sh term (-p) (-r) (path/to/profile)
 -p - look for a production container
 -r - connect as the "root" user

Example:
$ ./cbot.sh term -r bishop.env
EOF
            exit 0
            ;;
        p )
            PROD="true"
            ;;
        r )
            DOCKUSER="-u root"
            ;;
        \?)
            [ "$OPT" != "h" ] && echo "Invalid option: $OPTARG"
            usage
            exit 0
            ;;
        esac
    done
    shift $((OPTIND -1))
    GOPHER_PROFILE=$1
    check_profile
    eval `read_profile`
    if [ ! "$PROD" ]
    then
        CONTAINERNAME="$CONTAINERNAME-dev"
    fi
    echo "Starting shell session in the $CONTAINERNAME container ..."
    exec docker exec -it $DOCKUSER $CONTAINERNAME /bin/bash
    ;;
pull )
    while getopts ":h" OPT; do
        case $OPT in
        h ) cat <<"EOF"
Pull the most recent development and production gopherbot containers:
./cbot.sh pull

Example:
$ ./cbot.sh pull
latest: Pulling from lnxjedi/gopherbot-dev
...
EOF
            exit 0
            ;;
        \?)
            [ "$OPT" != "h" ] && echo "Invalid option: $OPTARG"
            usage
            exit 0
            ;;
        esac
    done
    docker pull ${IMAGE_NAME}-dev:${IMAGE_TAG}
    docker pull ${IMAGE_NAME}:${IMAGE_TAG}
    exit 0
    ;;
preview )
    CONTAINERNAME='floyd-gopherbot-preview'
    while getopts ":hru" OPT; do
        case $OPT in
        h ) cat <<"EOF"
Preview the Gopherbot IDE and Floyd, the default robot:
./cbot.sh preview (-u) (-r)
 -u - pull the latest container version first
 -r - stop and remove the preview container
(Note: you'll need to connect to the localhost interface, open a terminal,
and run 'gopherbot')
EOF
            exit 0
            ;;
        r )
            docker stop $CONTAINERNAME >/dev/null && docker rm $CONTAINERNAME >/dev/null
            echo "Removed"
            exit 0
            ;;
        u )
            PULL="true"
            ;;
        \?)
            [ "$OPT" != "h" ] && echo "Invalid option: $OPTARG"
            usage
            exit 0
            ;;
        esac
    done

    if STATUS=$(docker inspect -f {{.State.Status}} $CONTAINERNAME 2>/dev/null)
    then
        echo "(found existing container '$CONTAINERNAME', re-using)"
        if [ "$STATUS" == "exited" ]
        then
            echo "Starting '$CONTAINERNAME':"
            docker start $CONTAINERNAME
            wait_for_container
            if [ ! "$SUCCESS" ]
            then
                echo "Timed out waiting for container to start"
                exit 1
            fi
        fi
        ACCESS_URL=$(docker inspect --format='{{index .Config.Labels "access"}}' $CONTAINERNAME)
        RANDOM_TOKEN=${ACCESS_URL##*=}
        show_access $ACCESS_URL
        exit 0
    fi

    if [ "$PULL" ]
    then
        docker pull $IMAGE_SPEC
    fi

    echo "Running '$CONTAINERNAME':"

    IMAGE_NAME="$IMAGE_NAME-dev"
    IMAGE_SPEC="$IMAGE_NAME:$IMAGE_TAG"

    RANDOM_TOKEN="$(openssl rand -hex 21)"
    docker run -d \
        -p 127.0.0.1:7777:7777 \
        -l type=gopherbot/robot \
        -l environment=robot/preview \
        -l access=$(get_access) \
        --name $CONTAINERNAME $IMAGE_SPEC \
        --connection-token $RANDOM_TOKEN
    wait_for_container
    if [ ! "$SUCCESS" ]
    then
        echo "Timed out waiting for container to start"
        exit 1
    fi

    show_access -p
    ;;
start )
    while getopts ":hup" OPT; do
        case $OPT in
        h ) cat <<"EOF"
Start a robot container:
./cbot.sh start (-u) (-p) (path/to/profile)
 -u - pull the latest container version first
 -p - start a production robot (minimal image)

Example:
$ ./cbot.sh start bishop.env
Running 'bishop':
Unable to find image 'ghcr.io/lnxjedi/gopherbot-dev:latest' locally
latest: Pulling from lnxjedi/gopherbot-dev
...
Copying /home/david/.ssh/id_rsa to bishop:/home/bot/.ssh/id_ssh ...
Access your dev environment at: http://localhost:7777/?workspace=/home/bot/gopherbot.code-workspace&tkn=XXXXXXX
EOF
            exit 0
            ;;
        u )
            PULL="true"
            ;;
        p )
            PROD="true"
            ;;
        \?)
            [ "$OPT" != "h" ] && echo "Invalid option: $OPTARG"
            usage
            exit 0
            ;;
        esac
    done
    shift $((OPTIND -1))

    GOPHER_PROFILE=$1
    check_profile
    eval `read_profile`

    if [ ! "$PROD" ]
    then
        IMAGE_NAME="$IMAGE_NAME-dev"
        CONTAINERNAME="$CONTAINERNAME-dev"
    fi
    IMAGE_SPEC="$IMAGE_NAME:$IMAGE_TAG"

    if STATUS=$(docker inspect -f {{.State.Status}} $CONTAINERNAME 2>/dev/null)
    then
        echo "(found existing container '$CONTAINERNAME', re-using)"
        if [ "$STATUS" == "exited" ]
        then
            echo "Starting '$CONTAINERNAME':"
            docker start $CONTAINERNAME
            wait_for_container
            if [ ! "$SUCCESS" ]
            then
                echo "Timed out waiting for container to start"
                exit 1
            fi
        fi
        if [ "$PROD" ]
        then
            echo "... started"
        else
            copy_ssh
            ACCESS_URL=$(docker inspect --format='{{index .Config.Labels "access"}}' $CONTAINERNAME)
            RANDOM_TOKEN=${ACCESS_URL##*=}
            show_access $ACCESS_URL
        fi
        exit 0
    fi

    if [ "$PULL" ]
    then
        docker pull $IMAGE_SPEC
    fi

    echo "Running '$CONTAINERNAME':"

    if [ "$PROD" ]
    then
        docker run -d \
            --env-file $GOPHER_PROFILE \
            -l type=gopherbot/robot \
            -l environment=robot/production \
            --name $CONTAINERNAME $IMAGE_SPEC
    else
        RANDOM_TOKEN="$(openssl rand -hex 21)"
        docker run -d \
            -p 127.0.0.1:7777:7777 \
            -p 127.0.0.1:8888:8888 \
            --env-file $GOPHER_PROFILE \
            -l type=gopherbot/robot \
            -l environment=robot/development \
            -l access=$(get_access) \
            --name $CONTAINERNAME $IMAGE_SPEC \
            --connection-token $RANDOM_TOKEN
    fi
    wait_for_container
    if [ ! "$SUCCESS" ]
    then
        echo "Timed out waiting for container to start"
        exit 1
    fi

    if [ ! "$PROD" ]
    then
        copy_ssh
        show_access
    fi
    ;;
* )
    echo "Invalid command: $COMMAND"
    usage
    exit 1
esac
