function(_worker_fix_ntgcalls_visibility)
  # ntgcalls sets hidden visibility on macOS, which breaks cgo linking
  # (symbols exist but aren't exported). Override after all subdirs run.
  if(TARGET ntgcalls)
    set_target_properties(ntgcalls PROPERTIES
      CXX_VISIBILITY_PRESET default
      C_VISIBILITY_PRESET default
      VISIBILITY_INLINES_HIDDEN OFF
    )
  endif()
endfunction()

# Run after the full configure step.
cmake_language(DEFER CALL _worker_fix_ntgcalls_visibility)

