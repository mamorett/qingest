#!/usr/bin/env python3
"""
qingest.py

Walk a directory (optionally recursively) of Markdown files, embed their
contents via an OpenAI-compatible embedding endpoint, and persist the
results into a Qdrant collection.

Usage examples
--------------
  # Embed all .md files in ./docs into collection "knowledge"
  python qingest.py --dir ./docs --collection knowledge

  # Create the collection if it doesn't exist
  python qingest.py --dir ./docs --collection knowledge \
      --embed-url http://127.0.0.1:8008/v1 \
      --qdrant-url http://localhost:6333 \
      --create-collection

  # Skip subdirectories (only top-level .md files)
  python qingest.py --dir ./docs --no-recursive

  # Custom chunking parameters
  python qingest.py --dir ./docs --chunk-size 1000 --chunk-overlap 200

  # Use a specific embedding model
  python qingest.py --dir ./docs --embed-model bge-large-en

  # Force re-embedding of already-indexed files
  python qingest.py --dir ./docs --force

  # Preview normalization without embedding anything
  python qingest.py --dir ./docs --normalize --preview
"""

from __future__ import annotations

import argparse
import difflib
import hashlib
import json
import logging
import os
import re
import sys
import time
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

from dotenv import load_dotenv

import requests
from qdrant_client import QdrantClient
from qdrant_client.models import Distance, VectorParams, PointStruct, Filter, FieldCondition, MatchAny, PayloadSchemaType

# LangChain imports for advanced chunking
from langchain_text_splitters import MarkdownTextSplitter

# Load .env into os.environ (CLI args still override these)
load_dotenv()

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)
log = logging.getLogger("embed_markdown")

# ---------------------------------------------------------------------------
# Data classes
# ---------------------------------------------------------------------------

@dataclass
class Chunk:
    """A text chunk ready for embedding."""
    file_path: str          # original file path (relative to --dir)
    chunk_index: int        # 0-based index within the file
    content: str            # raw text
    file_hash: str = ""     # SHA256 of the source file, stored as top-level DB field
    metadata: dict = None   # extra metadata

    def __post_init__(self):
        if self.metadata is None:
            self.metadata = {}


# ---------------------------------------------------------------------------
# Text Normalization
# ---------------------------------------------------------------------------

def normalize_text(text: str) -> str:
    """Normalize text by removing non-printing characters, collapsing newlines, and repairing broken lines."""
    # Remove BOM if present
    text = text.lstrip('\ufeff')
    
    # Standardize newlines
    text = text.replace('\r\n', '\n').replace('\r', '\n')
    
    # Remove control characters (ASCII 0-31 except tab \t (9) and newline \n (10), plus DEL 127)
    text = re.sub(r'[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]', '', text)
    
    # Repair broken paragraph/line spacing
    lines = text.split('\n')
    output = []
    i = 0
    while i < len(lines):
        line = lines[i].strip()
        
        # If the current line is empty, see if we should join previous and next line
        if not line:
            if output and i + 1 < len(lines):
                prev_line = output[-1].strip()
                next_line = lines[i+1].strip()
                
                if prev_line and next_line:
                    is_prev_md = prev_line.startswith(('#', '-', '*', '>', '1.', '2.', '3.', '4.', '5.', '6.', '7.', '8.', '9.'))
                    is_next_md = next_line.startswith(('#', '-', '*', '>', '1.', '2.', '3.', '4.', '5.', '6.', '7.', '8.', '9.'))
                    
                    ends_with_terminator = prev_line[-1] in ('.', '!', '?', ':', ';') if prev_line else False
                    starts_with_lowercase = next_line[0].islower() if next_line else False
                    
                    if not is_prev_md and not is_next_md:
                        if not ends_with_terminator or starts_with_lowercase:
                            output[-1] = prev_line + " " + next_line
                            i += 2
                            continue
            
            output.append(lines[i])
            i += 1
        else:
            # If the current line has text, check if it should be joined with the previous line (repairing single newline wraps)
            if output and output[-1].strip():
                prev_line = output[-1].strip()
                is_prev_md = prev_line.startswith(('#', '-', '*', '>', '1.', '2.', '3.', '4.', '5.', '6.', '7.', '8.', '9.'))
                is_curr_md = line.startswith(('#', '-', '*', '>', '1.', '2.', '3.', '4.', '5.', '6.', '7.', '8.', '9.'))
                
                ends_with_terminator = prev_line[-1] in ('.', '!', '?', ':', ';') if prev_line else False
                starts_with_lowercase = line[0].islower() if line else False
                
                if not is_prev_md and not is_curr_md:
                    if not ends_with_terminator or starts_with_lowercase:
                        output[-1] = prev_line + " " + line
                        i += 1
                        continue
            
            output.append(lines[i])
            i += 1
            
    # Reconstruct text
    text = '\n'.join(output)
    
    # Finally, collapse 3 or more consecutive newlines to exactly two newlines
    text = re.sub(r'\n\s*\n\s*\n+', '\n\n', text)
    
    return text


# ---------------------------------------------------------------------------
# Chunking logic
# ---------------------------------------------------------------------------

def chunk_markdown_text(
    file_path: str,
    text: str,
    file_hash: str,
    chunk_size: int = 800,
    chunk_overlap: int = 200,
) -> list[Chunk]:
    """Split a markdown file's text into semantic chunks using LangChain."""
    splitter = MarkdownTextSplitter(
        chunk_size=chunk_size,
        chunk_overlap=chunk_overlap,
    )
    
    # Split the markdown text
    st_chunks = splitter.split_text(text)
    
    return [
        Chunk(
            file_path=file_path,
            chunk_index=i,
            content=chunk_text,
            file_hash=file_hash,
            metadata={
                "source_file": file_path,
                "total_chunks": len(st_chunks),
            },
        )
        for i, chunk_text in enumerate(st_chunks)
        if chunk_text.strip()
    ]


# ---------------------------------------------------------------------------
# Embedding (OpenAI-compatible endpoint)
# ---------------------------------------------------------------------------

_EMBED_MODEL_DEFAULT = "bge-m3"

def _embed_batch(
    texts: list[str],
    embed_url: str,
    model: str = _EMBED_MODEL_DEFAULT,
    batch_size: int = 128,
) -> list[list[float]]:
    """
    Call an OpenAI-compatible /v1/embeddings endpoint in batches.

    Returns a list of embedding vectors, one per input text.
    """
    all_embeddings: list[list[float]] = []

    for i in range(0, len(texts), batch_size):
        batch = texts[i : i + batch_size]
        log.info(
            "Embedding batch %d–%d / %d",
            i + 1,
            min(i + batch_size, len(texts)),
            len(texts),
        )

        payload = {
            "model": model,
            "input": batch,
        }

        resp = requests.post(
            f"{embed_url}/embeddings",
            json=payload,
            timeout=300,
        )
        resp.raise_for_status()

        body = resp.json()
        # OpenAI-compatible shape: { data: [ { embedding: [...] }, ... ] }
        embeddings = [item["embedding"] for item in body["data"]]
        all_embeddings.extend(embeddings)

    return all_embeddings


# ---------------------------------------------------------------------------
# Qdrant DB interaction
# ---------------------------------------------------------------------------

def qdrant_create_collection_if_not_exists(
    client: QdrantClient,
    collection: str,
    vector_dim: int,
) -> None:
    """Create collection if it doesn't already exist and add payload index."""
    try:
        if not client.collection_exists(collection_name=collection):
            log.info("Creating collection '%s' (vector dim=%d).", collection, vector_dim)
            client.create_collection(
                collection_name=collection,
                vectors_config=VectorParams(size=vector_dim, distance=Distance.COSINE),
            )
            # Create index on file_path for fast filtering/scrolling
            client.create_payload_index(
                collection_name=collection,
                field_name="file_path",
                field_schema=PayloadSchemaType.KEYWORD,
            )
            log.info("Collection '%s' created and payload index ensured.", collection)
        else:
            log.info("Collection '%s' already exists.", collection)
    except Exception as exc:
        log.error("Failed to ensure/create Qdrant collection '%s': %s", collection, exc)
        raise


def qdrant_get_indexed_paths(
    client: QdrantClient,
    collection: str,
    filter_paths: Optional[list[str]] = None,
) -> dict[str, str]:
    """Retrieve indexed file paths and their hashes from Qdrant."""
    if not filter_paths:
        return {}

    path_hashes: dict[str, str] = {}
    try:
        # Scroll points matching the filter_paths in batches using pagination
        offset = None
        while True:
            points, offset = client.scroll(
                collection_name=collection,
                scroll_filter=Filter(
                    must=[
                        FieldCondition(
                            key="file_path",
                            match=MatchAny(any=filter_paths),
                        )
                    ]
                ),
                limit=1000,
                with_payload=["file_path", "file_hash"],
                with_vectors=False,
                offset=offset,
            )
            for point in points:
                payload = point.payload or {}
                fp = payload.get("file_path")
                fh = payload.get("file_hash")
                if fp:
                    path_hashes[fp] = fh if fh else "__legacy__"
            
            if offset is None:
                break
    except Exception as exc:
        log.warning("Failed to query indexed paths from Qdrant collection '%s': %s", collection, exc)

    log.debug("Found %d indexed path(s) in Qdrant collection '%s'.", len(path_hashes), collection)
    return path_hashes


def qdrant_delete_by_paths(
    client: QdrantClient,
    collection: str,
    file_paths: set[str],
) -> int:
    """Delete all records matching any of the given file_path values."""
    if not file_paths:
        return 0
    try:
        client.delete(
            collection_name=collection,
            points_selector=Filter(
                must=[
                    FieldCondition(
                        key="file_path",
                        match=MatchAny(any=list(file_paths)),
                    )
                ]
            ),
        )
        log.debug("Deleted records for file_paths=%s.", file_paths)
        return len(file_paths)
    except Exception as exc:
        log.error("Failed to delete records for file_paths in Qdrant: %s", exc)
        return 0


def qdrant_store_embeddings(
    client: QdrantClient,
    collection: str,
    chunks: list[Chunk],
    embeddings: list[list[float]],
    dry_run: bool = False,
) -> int:
    """Store chunks + embeddings into Qdrant."""
    from datetime import datetime, timezone
    count = 0
    now = datetime.now(timezone.utc)
    points = []
    
    if embeddings:
        dims = [len(e) for e in embeddings]
        unique_dims = set(dims)
        log.info("Preparing to upsert %d points to collection '%s' with vector dimensions: %s (min: %d, max: %d)",
                 len(embeddings), collection, unique_dims, min(dims), max(dims))
    
    for chunk, emb in zip(chunks, embeddings):
        if dry_run:
            log.info("[DRY-RUN] Would insert chunk from %s (idx %d, %d dims)",
                     chunk.file_path, chunk.chunk_index, len(emb))
            count += 1
            continue
            
        point_id = str(uuid.uuid4())
        payload = {
            "content": chunk.content,
            "file_path": chunk.file_path,
            "file_hash": chunk.file_hash,
            "chunk_index": chunk.chunk_index,
            "metadata": chunk.metadata,
            "indexed_at": now.isoformat(),
        }
        points.append(PointStruct(id=point_id, vector=emb, payload=payload))
        
    if dry_run or not points:
        return count

    try:
        # Batch upserts to prevent exceeding Qdrant's payload size limit (default 32MB)
        upsert_batch_size = 100
        for idx in range(0, len(points), upsert_batch_size):
            batch_points = points[idx : idx + upsert_batch_size]
            client.upsert(collection_name=collection, points=batch_points)
        count = len(points)
    except Exception as exc:
        log.error("Failed to upsert chunks to Qdrant collection '%s': %s", collection, exc)
        if hasattr(exc, "content"):
            log.error("Response content: %s", exc.content)
        raise
        
    return count


# ---------------------------------------------------------------------------
# File discovery
# ---------------------------------------------------------------------------

def discover_md_files(
    directory: str,
    recursive: bool = True,
    extension: str = ".md",
) -> list[str]:
    """Return sorted list of .md file paths under *directory*."""
    root = Path(directory).resolve()
    pattern = "**" if recursive else ""
    files = sorted(root.glob(f"{pattern}/*{extension}"))
    return [str(f) for f in files]


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    parser = argparse.ArgumentParser(
        description="Embed markdown files into Qdrant via an embedding API.",
    )
    parser.add_argument(
        "--dir", "-d",
        required=True,
        help="Root directory containing .md files.",
    )
    parser.add_argument(
        "--embed-url",
        default=os.environ.get("QDRANT_EMBED_URL", os.environ.get("SURREAL_EMBED_URL", "http://127.0.0.1:8008/v1")),
        help="Base URL of the OpenAI-compatible embedding API.",
    )
    parser.add_argument(
        "--embed-model",
        default=os.environ.get("QDRANT_EMBED_MODEL", os.environ.get("SURREAL_EMBED_MODEL", _EMBED_MODEL_DEFAULT)),
        help="Embedding model name (default: %s)." % _EMBED_MODEL_DEFAULT,
    )
    parser.add_argument(
        "--qdrant-url",
        default=os.environ.get("QDRANT_URL", "http://localhost:6333"),
        help="Qdrant API URL.",
    )
    parser.add_argument(
        "--qdrant-api-key",
        default=os.environ.get("QDRANT_API_KEY", None),
        help="Qdrant API Key (optional).",
    )
    parser.add_argument(
        "--collection",
        default=os.environ.get("QDRANT_COLLECTION", os.environ.get("SURREAL_TABLE", "mdchunk")),
        help="Qdrant collection to store chunks into.",
    )
    parser.add_argument(
        "--no-recursive",
        action="store_true",
        help="Only scan the top-level directory (no subdirectories).",
    )
    parser.add_argument(
        "--chunk-size",
        type=int,
        default=800,
        help="Target chunk size in characters.",
    )
    parser.add_argument(
        "--chunk-overlap",
        type=int,
        default=200,
        help="Overlap between chunks in characters.",
    )
    parser.add_argument(
        "--create-collection",
        action="store_true",
        help="Create the target collection if it doesn't exist.",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Walk files, chunk, embed, but do NOT write to Qdrant.",
    )
    parser.add_argument(
        "--batch-size",
        type=int,
        default=128,
        help="Number of texts to embed per API call.",
    )
    parser.add_argument(
        "--doc-batch-size",
        type=int,
        default=5,
        help="Number of documents to process and ingest as a single batch (default: 5).",
    )
    parser.add_argument(
        "--force", "-f",
        action="store_true",
        help="Re-embed and re-insert files that are already in the DB (delete old records first).",
    )
    parser.add_argument(
        "--normalize",
        action="store_true",
        help="Normalize text (removes non-printing characters, collapses multi-newlines).",
    )
    parser.add_argument(
        "--preview",
        action="store_true",
        help="Preview normalization diffs for the first 5 markdown files without actual ingestion.",
    )
    parser.add_argument(
        "--verbose", "-v",
        action="store_true",
        help="Verbose (debug) logging.",
    )

    args = parser.parse_args()

    if args.verbose:
        logging.getLogger().setLevel(logging.DEBUG)

    directory = args.dir
    if not os.path.isdir(directory):
        log.error("Directory '%s' does not exist.", directory)
        sys.exit(1)

    # --- 1. Discover files ---
    files = discover_md_files(directory, recursive=not args.no_recursive)
    log.info("Found %d markdown file(s) in '%s'.", len(files), directory)
    
    if not files:
        log.warning("No .md files found. Exiting.")
        sys.exit(0)

    # --- 2. Normalization Preview Mode ---
    if args.preview:
        preview_limit = 10
        log.info("=== Normalization Preview (First %d Files) ===", preview_limit)
        for f in files[:preview_limit]:
            rel = os.path.relpath(f, directory)
            try:
                with open(f, "r", encoding="utf-8", errors="replace") as file_obj:
                    orig = file_obj.read()
            except Exception as exc:
                log.error("Failed to read '%s': %s", rel, exc)
                continue

            norm = normalize_text(orig)
            orig_lines = orig.splitlines()
            norm_lines = norm.splitlines()
            orig_empty = sum(1 for line in orig_lines if not line.strip())
            norm_empty = sum(1 for line in norm_lines if not line.strip())

            print(f"\n" + "="*80)
            print(f"File: {rel}")
            print(f"Stats:")
            print(f"  Characters: {len(orig)} -> {len(norm)} (delta: {len(norm) - len(orig)})")
            print(f"  Total Lines: {len(orig_lines)} -> {len(norm_lines)} (delta: {len(norm_lines) - len(orig_lines)})")
            print(f"  Empty Lines: {orig_empty} -> {norm_empty} (delta: {norm_empty - orig_empty})")
            print("="*80)
            
            diff = list(difflib.unified_diff(
                orig.splitlines(keepends=True),
                norm.splitlines(keepends=True),
                fromfile='Original',
                tofile='Normalized',
                n=10
            ))
            
            if diff:
                print("Changes made by normalization:")
                print("".join(diff))
                print("-" * 80)
            
            print("Normalized Content Preview (First 50 Lines):")
            print("".join(norm.splitlines(keepends=True)[:50]))
            print("="*80)
        log.info("Preview finished. Exiting without database ingestion.")
        sys.exit(0)

    log.info("Using Qdrant: %s (Collection: %s)", args.qdrant_url, args.collection)

    # Initialize Qdrant client
    client = QdrantClient(url=args.qdrant_url, api_key=args.qdrant_api_key, timeout=60.0)

    collection_ensured = False

    # --- 3. Process in batches of documents ---
    doc_batch_size = args.doc_batch_size
    total_files = len(files)
    total_inserted = 0
    total_skipped = 0

    log.info("Processing %d files in batches of %d documents.", total_files, doc_batch_size)

    for i in range(0, total_files, doc_batch_size):
        batch_files = files[i : i + doc_batch_size]
        batch_num = (i // doc_batch_size) + 1
        total_batches = (total_files + doc_batch_size - 1) // doc_batch_size
        
        log.info("--- Batch %d/%d (Files %d-%d of %d) ---", 
                 batch_num, total_batches, i+1, min(i+doc_batch_size, total_files), total_files)
        
        # a. Read contents, apply optional normalization, and compute relative paths and hashes
        batch_contents: dict[str, str] = {}
        batch_hashes: dict[str, str] = {}
        for f in batch_files:
            rel = os.path.relpath(f, directory)
            try:
                with open(f, "r", encoding="utf-8", errors="replace") as file_obj:
                    content = file_obj.read()
            except Exception as exc:
                log.error("Failed to read file '%s': %s", f, exc)
                continue
            
            if args.normalize:
                content = normalize_text(content)
                
            batch_contents[rel] = content
            batch_hashes[rel] = hashlib.sha256(content.encode('utf-8', errors='replace')).hexdigest()

        indexed_file_hashes: dict[str, str] = {}
        if not args.dry_run:
            try:
                indexed_file_hashes = qdrant_get_indexed_paths(
                    client, args.collection, filter_paths=list(batch_hashes.keys())
                )
            except Exception as exc:
                log.warning("Could not query existing hashes for batch (will process all): %s", exc)

        files_to_process_this_batch: list[tuple[str, str, str]] = [] # (abs, rel, hash)

        for f in batch_files:
            rel = os.path.relpath(f, directory)
            if rel not in batch_hashes:
                continue
            current_hash = batch_hashes[rel]
            exists_in_db = rel in indexed_file_hashes
            stored_hash = indexed_file_hashes.get(rel)

            if exists_in_db:
                log.info("🔍 DB check: '%s' | Stored Hash: %s | Current Hash: %s | Match: %s", 
                         rel, stored_hash, current_hash, stored_hash == current_hash)
            else:
                log.info("🔍 DB check: '%s' | Not found in collection", rel)

            should_process = False
            reason = ""

            if not exists_in_db:
                should_process = True
                reason = "New file"
            elif args.force:
                should_process = True
                reason = "Force replace (--force)"
            elif stored_hash == "__legacy__":
                # Already in DB but predates hash tracking — skip unless --force
                log.info("⏭️  %s: Already indexed (legacy record). Skipping.", rel)
                should_process = False
            elif stored_hash != current_hash:
                should_process = True
                reason = f"Modified (hash mismatch: stored={stored_hash}, current={current_hash})"
            else:
                log.info("⏭️  %s: Identical (hash match). Skipping.", rel)
                should_process = False

            if should_process:
                log.info("🚀 Stage for ingestion: %s (%s)", rel, reason)
                files_to_process_this_batch.append((f, rel, current_hash))
            else:
                total_skipped += 1

        if not files_to_process_this_batch:
            log.info("No files to update in this batch.")
            continue

        if not args.dry_run:
            paths_to_delete = {rel for _, rel, _ in files_to_process_this_batch}
            if paths_to_delete:
                log.info("Cleaning old points from collection for: %s", paths_to_delete)
                qdrant_delete_by_paths(client, args.collection, paths_to_delete)

        # b. Chunk the pre-read and normalized text
        batch_chunks: list[Chunk] = []
        for abs_path, rel_path, f_hash in files_to_process_this_batch:
            text = batch_contents[rel_path]
            chunks = chunk_markdown_text(rel_path, text, f_hash, args.chunk_size, args.chunk_overlap)
            batch_chunks.extend(chunks)
            log.info("File '%s' → %d chunk(s)", rel_path, len(chunks))

        if not batch_chunks:
            continue

        # c. Embed
        log.info("Embedding %d chunks...", len(batch_chunks))
        texts = [c.content for c in batch_chunks]
        try:
            embeddings = _embed_batch(texts, args.embed_url, args.embed_model, args.batch_size)
        except Exception as exc:
            log.error("Embedding failed for batch %d: %s", batch_num, exc)
            continue

        # d. Ensure collection schema (once, after we know vector dim)
        if args.create_collection and not collection_ensured:
            vector_dim = len(embeddings[0])
            try:
                qdrant_create_collection_if_not_exists(client, args.collection, vector_dim)
                collection_ensured = True
            except Exception as exc:
                log.error("Failed to create Qdrant collection: %s", exc)
                sys.exit(1)

        # e. Store
        if not args.dry_run:
            inserted = qdrant_store_embeddings(
                client, args.collection, batch_chunks, embeddings,
                dry_run=False,
            )
            total_inserted += inserted
            log.info("Batch %d: Inserted %d record(s).", batch_num, inserted)
        else:
            qdrant_store_embeddings(
                client, args.collection, batch_chunks, embeddings,
                dry_run=True,
            )
            log.info("Batch %d: Dry-run complete.", batch_num)

    if not args.dry_run:
        log.info("Total inserted: %d record(s), skipped: %d file(s).",
                 total_inserted, total_skipped)
    else:
        log.info("Dry-run complete. No data written to Qdrant.")

    log.info("Done.")


if __name__ == "__main__":
    main()