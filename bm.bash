bm() {
    if (($# > 0)); then
        command bm "$@"
        return $?
    fi

    local bm_fd bm_action bm_alias bm_path
    exec {bm_fd}< <(BM_INTERNAL_SELECT=1 command bm)

    if ! IFS= read -r -d '' bm_action <&"$bm_fd"; then
        exec {bm_fd}<&-
        return 1
    fi
    if [[ $bm_action == cancelled ]]; then
        exec {bm_fd}<&-
        return 0
    fi
    if [[ $bm_action != selected ]] ||
        ! IFS= read -r -d '' bm_alias <&"$bm_fd" ||
        ! IFS= read -r -d '' bm_path <&"$bm_fd"; then
        exec {bm_fd}<&-
        return 1
    fi
    exec {bm_fd}<&-

    if cd -- "$bm_path"; then
        BM_INTERNAL_MARK_USED=1 command bm "$bm_alias"
    else
        return $?
    fi
}
