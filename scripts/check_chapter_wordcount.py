#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Chapter word-count checker
Checks the word count of a chapter file and warns when it is below 3000 characters
"""

import re
import sys
from pathlib import Path

# Fix Windows console encoding issues
if sys.platform == 'win32':
    import io
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')


def count_chinese_words(text: str) -> int:
    """Count Chinese characters, excluding punctuation and Markdown markers."""
    text = re.sub(r'#{1,6}\s*', '', text)
    text = re.sub(r'\*\*(.*?)\*\*', r'\1', text)
    text = re.sub(r'\*(.*?)\*', r'\1', text)
    text = re.sub(r'~~(.*?)~~', r'\1', text)
    text = re.sub(r'`(.*?)`', r'\1', text)
    text = re.sub(r'\[(.*?)\]\(.*?\)', r'\1', text)

    chinese_chars = re.findall(r'[\u4e00-\u9fff]', text)
    return len(chinese_chars)


def extract_content_from_chapter(file_path: Path) -> str:
    """Extract the chapter body from a chapter file, excluding headings and other metadata."""
    content = file_path.read_text(encoding='utf-8')
    lines = content.split('\n')

    content_start = 0
    for i, line in enumerate(lines):
        if line.startswith('#') and ('章' in line or 'Chapter' in line):
            content_start = i + 1
            break

    return '\n'.join(lines[content_start:])


def check_chapter(file_path: str, min_words: int = 3000) -> dict:
    """Check the word count for a single chapter."""
    path = Path(file_path)
    if not path.exists():
        return {
            'file': str(path),
            'exists': False,
            'word_count': 0,
            'status': 'error',
            'message': f'File not found: {file_path}',
        }

    main_content = extract_content_from_chapter(path)
    word_count = count_chinese_words(main_content)
    status = 'pass' if word_count >= min_words else 'fail'
    message = f'Word count: {word_count}'
    if word_count >= min_words:
        message += ' (✓ pass)'
    else:
        message += f' (✗ too short, needs at least {min_words} characters)'

    return {
        'file': str(path),
        'exists': True,
        'word_count': word_count,
        'status': status,
        'message': message,
    }


def check_all_chapters(directory: str, pattern: str = '*.md', min_words: int = 3000) -> list:
    """Check all chapter files in a directory that match the pattern."""
    dir_path = Path(directory)
    if not dir_path.exists():
        print(f'Error: directory does not exist - {directory}')
        return []

    chapter_files = sorted(dir_path.glob(pattern))
    return [check_chapter(str(chapter_file), min_words) for chapter_file in chapter_files]


def print_results(results: list, min_words: int = 3000) -> None:
    """Print the check results."""
    if not results:
        print('No chapter files found')
        return

    total_words = 0
    passed = 0
    failed = 0

    print('\n' + '=' * 60)
    print('Chapter word-count report')
    print('=' * 60)

    for result in results:
        if not result['exists']:
            print(f'\n❌ {result["file"]}')
            print(f'   {result["message"]}')
            continue

        total_words += result['word_count']
        if result['status'] == 'pass':
            passed += 1
            icon = '✅'
        else:
            failed += 1
            icon = '⚠️ '

        print(f'\n{icon} {Path(result["file"]).name}')
        print(f'   {result["message"]}')

    print('\n' + '-' * 60)
    print(f'Total: {len(results)} chapters | {passed} passed | {failed} failed | Total words: {total_words:,}')
    print('-' * 60)

    if failed > 0:
        print(f'\n⚠️  {failed} chapters are below {min_words} characters. Try expanding with:')
        print('   - Add more sensory and scene detail')
        print('   - Add more dialogue')
        print('   - Expand inner thoughts and emotions')
        print('   - Add background context')
        print('\n   Reference: references/content-expansion.md')


def main() -> None:
    """Main entry point."""
    if len(sys.argv) < 2:
        print('Usage:')
        print('  Check one chapter: python check_chapter_wordcount.py <chapter-file-path> [min-words]')
        print('  Check all chapters: python check_chapter_wordcount.py --all <directory-path> [min-words]')
        print('')
        print('Examples:')
        print('  python check_chapter_wordcount.py novels/story/chapter-01.md')
        print('  python check_chapter_wordcount.py novels/story/chapter-01.md 3500')
        print('  python check_chapter_wordcount.py --all novels/story')
        print('  python check_chapter_wordcount.py --all novels/story 3500')
        return

    if sys.argv[1] == '--all':
        if len(sys.argv) < 3:
            print('Error: --all requires a directory path')
            return
        directory = sys.argv[2]
        min_words = int(sys.argv[3]) if len(sys.argv) > 3 else 3000
        results = check_all_chapters(directory, min_words=min_words)
        print_results(results, min_words)
        return

    file_path = sys.argv[1]
    min_words = int(sys.argv[2]) if len(sys.argv) > 2 else 3000
    result = check_chapter(file_path, min_words)
    print_results([result], min_words)


if __name__ == '__main__':
    main()
